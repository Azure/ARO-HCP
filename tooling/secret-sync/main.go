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
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
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

func readAndChunkData(inputReader io.Reader) [][]byte {
	returnBytes := make([][]byte, 0)
	reader := bufio.NewReader(inputReader)

	for {
		data := make([]byte, chunkSizeBytes)
		n, err := reader.Read(data)
		if err == io.EOF {
			break
		} else if err != nil {
			log.Fatalf("Problems reading from input: %s", err)
		}
		returnBytes = append(returnBytes, data[:n])
	}
	return returnBytes
}

func persistEncryptedChunks(encryptedChunks [][]byte) {
	outputFile, err := os.Create(os.Getenv(outputFileEnvKey))
	if err != nil {
		log.Fatal(err)
	}
	defer outputFile.Close()

	for _, c := range encryptedChunks {
		encodedChunk := make([]byte, base64.StdEncoding.EncodedLen(len(c)))
		base64.StdEncoding.Encode(encodedChunk, c)
		_, err := outputFile.Write(encodedChunk)
		if err != nil {
			log.Fatal(err)
		}
		_, err = outputFile.Write([]byte(chunkDelemiter))
		if err != nil {
			log.Fatal(err)
		}
	}

}

func encryptData(secretMessage []byte) ([]byte, error) {
	pubPEMData, err := os.ReadFile(os.Getenv("PUBLIC_KEY_FILE"))
	if err != nil {
		log.Fatal(fmt.Errorf("Error while reading public key file %s: %v", os.Getenv("PUBLIC_KEY_FILE"), err))
	}

	block, _ := pem.Decode(pubPEMData)
	if block == nil || block.Type != "PUBLIC KEY" {
		log.Fatal("failed to decode PEM block containing public key")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		log.Fatal(err)
	}

	label := []byte{}
	rng := rand.Reader

	return rsa.EncryptOAEP(sha256.New(), rng, pub.(*rsa.PublicKey), secretMessage, label)
}

func decryptData(client *azkeys.Client, encryptedMessage []byte) []byte {
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
		log.Fatal(err)
	}

	return d.Result
}

func persistSecret(client *azsecrets.Client, secret []byte) {
	secretToSet := os.Getenv(secretToSetEnvKey)
	currentSecret, err := client.GetSecret(
		context.Background(),
		secretToSet,
		"",
		&azsecrets.GetSecretOptions{})
	if err != nil && !strings.Contains(err.Error(), "SecretNotFound") {
		log.Fatal(err)
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
				log.Fatal(err)
			}
		} else {
			fmt.Println("Skipped due to dry run")
		}
	} else {
		fmt.Println("Secret up to date")
	}
}

func readEncryptedChunks() [][]byte {
	chunkedData, err := os.ReadFile(os.Getenv(inputFileKeyEnv))
	if err != nil {
		log.Fatal(err)
	}
	return bytes.Split(chunkedData, []byte(chunkDelemiter))
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Need to provide mode parameter encrypt/decrypt")
	}
	mode := os.Args[1]

	if mode == "encrypt" {
		encryptedChunks := make([][]byte, 0)
		for _, c := range readAndChunkData(os.Stdin) {
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
			persistEncryptedChunks(encryptedChunks)
		}
		os.Exit(0)
	} else if mode == "decrypt" {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			log.Fatal(err)
		}

		keyClient, err := azkeys.NewClient(fmt.Sprintf("https://%s.vault.azure.net", os.Getenv(vaultNameEnvKey)), cred, nil)
		if err != nil {
			log.Fatal(err)
		}
		decryptedChunks := make([][]byte, 0)

		for _, c := range readEncryptedChunks() {
			if len(c) > 0 {
				dst := make([]byte, base64.StdEncoding.DecodedLen(len(c)))
				base64.StdEncoding.Decode(dst, c)
				decryptedChunks = append(decryptedChunks, decryptData(keyClient, dst))
			}
		}
		secretsClient, err := azsecrets.NewClient(fmt.Sprintf("https://%s.vault.azure.net", os.Getenv(vaultNameEnvKey)), cred, nil)
		if err != nil {
			log.Fatal(err)
		}
		joinedMessage := bytes.Join(decryptedChunks, []byte{})
		fmt.Printf("Data decrypted, persisting to: %s\n", os.Getenv(secretToSetEnvKey))
		persistSecret(secretsClient, joinedMessage)
		os.Exit(0)
	}

	log.Fatalf("Invalid mode %s", mode)
}
