package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/azauth"
)

// conservative
var chunkSizeBytes = 400
var chunkDelemiter = "\n"

const (
	dryRunEnvKey        = "DRY_RUN"
	inputFileKeyEnv     = "INPUT_FILE"
	outputFileEnvKey    = "OUTPUT_FILE"
	publicKetFileEnvKey = "PUBLIC_KEY_FILE"
	encryptionKeyEnvKey = "ENCRYPTION_KEY"
	secretToSetEnvKey   = "SECRET_TO_SET"
	vaultNameEnvKey     = "KEYVAULT"
)

func readAndChunkData(inputReader io.Reader) ([][]byte, error) {
	returnBytes := make([][]byte, 0)
	reader := bufio.NewReader(inputReader)

	for {
		data := make([]byte, chunkSizeBytes)
		n, err := reader.Read(data)
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("problems reading from input: %v", err)
		}
		returnBytes = append(returnBytes, data[:n])
	}
	return returnBytes, nil
}

func persistEncryptedChunks(encryptedChunks [][]byte) error {
	outputFile, err := os.Create(os.Getenv(outputFileEnvKey))
	if err != nil {
		return fmt.Errorf("error creating output file %v", err)
	}
	defer outputFile.Close()

	for _, c := range encryptedChunks {
		encodedChunk := make([]byte, base64.StdEncoding.EncodedLen(len(c)))
		base64.StdEncoding.Encode(encodedChunk, c)
		_, err := outputFile.Write(encodedChunk)
		if err != nil {
			return fmt.Errorf("error writing encoded chunk %v", err)
		}
		_, err = outputFile.Write([]byte(chunkDelemiter))
		if err != nil {
			return fmt.Errorf("error writing delimiter %v", err)
		}
	}
	return nil
}

func encryptData(secretMessage []byte) ([]byte, error) {
	pubPEMData, err := os.ReadFile(os.Getenv("PUBLIC_KEY_FILE"))
	if err != nil {
		return nil, fmt.Errorf("error while reading public key file %s: %v", os.Getenv("PUBLIC_KEY_FILE"), err)
	}

	block, _ := pem.Decode(pubPEMData)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("failed to decode PEM block containing public key")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("error while parsing public key %v", err)
	}

	label := []byte{}
	rng := rand.Reader

	return rsa.EncryptOAEP(sha256.New(), rng, pub.(*rsa.PublicKey), secretMessage, label)
}

func decryptData(client *azkeys.Client, encryptedMessage []byte) ([]byte, error) {
	d, err := client.Decrypt(
		context.Background(),
		os.Getenv(encryptionKeyEnvKey),
		"",
		azkeys.KeyOperationParameters{
			Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP256),
			Value:     encryptedMessage,
		},
		&azkeys.DecryptOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("error decoding secret %v", err)
	}

	return d.Result, nil
}

func persistSecret(client *azsecrets.Client, secret []byte) error {
	secretToSet := os.Getenv(secretToSetEnvKey)
	currentSecret, err := client.GetSecret(
		context.Background(),
		secretToSet,
		"",
		&azsecrets.GetSecretOptions{})
	if err != nil && !strings.Contains(err.Error(), "SecretNotFound") {
		return fmt.Errorf("error getting secret %v", err)
	}

	if currentSecret.Value == nil || *currentSecret.Value != string(secret) {
		fmt.Println("Secret needs update")
		if os.Getenv(dryRunEnvKey) != "true" {
			_, err := client.SetSecret(
				context.Background(),
				secretToSet,
				azsecrets.SetSecretParameters{
					Value: to.Ptr(string(secret)),
				},
				nil,
			)
			if err != nil {
				return fmt.Errorf("error setting secret %v", err)
			}
		} else {
			fmt.Println("Skipped due to dry run")
		}
	} else {
		fmt.Println("Secret up to date")
	}
	return nil
}

func readEncryptedChunks() ([][]byte, error) {
	chunkedData, err := os.ReadFile(os.Getenv(inputFileKeyEnv))
	if err != nil {
		return nil, fmt.Errorf("error reading input file %v", err)
	}
	return bytes.Split(chunkedData, []byte(chunkDelemiter)), nil
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Need to provide mode parameter encrypt/decrypt")
	}
	mode := os.Args[1]

	switch mode {
	case "encrypt":
		{
			encryptedChunks := make([][]byte, 0)
			plainChunks, err := readAndChunkData(os.Stdin)
			if err != nil {
				log.Fatal(err)
			}
			for _, c := range plainChunks {
				encryptedChunk, err := encryptData(c)
				if err != nil {
					log.Fatal(err)
				}
				encryptedChunks = append(encryptedChunks, encryptedChunk)
			}
			fmt.Printf("Encrypted data, persisting to: %s\n", os.Getenv(outputFileEnvKey))
			if os.Getenv(dryRunEnvKey) == "true" {
				fmt.Println("... skiped due to dry run")
			} else {
				if err := persistEncryptedChunks(encryptedChunks); err != nil {
					log.Fatal(err)
				}
			}
			os.Exit(0)
		}
	case "decrypt":
		{
			chain, err := azauth.GetAzureTokenCredentials()
			if err != nil {
				log.Fatal(fmt.Errorf("error getting credentials %v", err))
			}

			keyClient, err := azkeys.NewClient(fmt.Sprintf("https://%s.vault.azure.net", os.Getenv(vaultNameEnvKey)), chain, nil)
			if err != nil {
				log.Fatal(fmt.Errorf("error getting azkeys client %v", err))
			}
			decryptedChunks := make([][]byte, 0)
			encryptedChunks, err := readEncryptedChunks()
			if err != nil {
				log.Fatal(err)
			}
			for _, c := range encryptedChunks {
				if len(c) > 0 {
					dst := make([]byte, base64.StdEncoding.DecodedLen(len(c)))
					if _, err = base64.StdEncoding.Decode(dst, c); err != nil {
						log.Fatal(err)
					}
					decryptedChunk, err := decryptData(keyClient, dst)
					if err != nil {
						log.Fatal(err)
					}
					decryptedChunks = append(decryptedChunks, decryptedChunk)
				}
			}
			secretsClient, err := azsecrets.NewClient(fmt.Sprintf("https://%s.vault.azure.net", os.Getenv(vaultNameEnvKey)), chain, nil)
			if err != nil {
				log.Fatal(fmt.Errorf("error getting azsecrets client %v", err))
			}
			joinedMessage := bytes.Join(decryptedChunks, []byte{})
			fmt.Printf("Data decrypted, persisting to: %s\n", os.Getenv(secretToSetEnvKey))
			if err := persistSecret(secretsClient, joinedMessage); err != nil {
				log.Fatal(err)
			}
			os.Exit(0)
		}
	default:
		log.Fatalf("Invalid mode %s", mode)
	}

}
