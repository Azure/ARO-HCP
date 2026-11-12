package register

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config/ev2config"
	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/ARO-Tools/tools/secret-sync/config"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		RawOptions: &cmdutils.RawOptions{},
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ConfigFile, "config-file", opts.ConfigFile, "Configuration for what to register, encoded as a JSON string.")
	cmd.Flags().StringVar(&opts.KeyVault, "keyvault", opts.KeyVault, "KeyVault name into which we will populate secrets.")
	cmd.Flags().StringVar(&opts.PublicKeyFile, "public-key-file", opts.PublicKeyFile, "File containing public key, if the configuration does not have one registered yet or it needs to be updated.")
	cmd.Flags().StringVar(&opts.SecretFile, "secret-file", opts.SecretFile, "File containing secret material to be encrypted.")
	cmd.Flags().StringVar(&opts.SecretName, "secret-name", opts.SecretName, "Name of the secret to be populated in KeyVault using this secret material.")
	for _, flag := range []string{"config-file", "public-key-file", "secret-file"} {
		if err := cmd.MarkFlagFilename(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	return cmdutils.BindOptions(opts.RawOptions, cmd)
}

// RawOptions holds input values.
type RawOptions struct {
	*cmdutils.RawOptions
	KeyVault      string
	ConfigFile    string
	PublicKeyFile string
	SecretFile    string
	SecretName    string
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
	*cmdutils.ValidatedOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before Config generation can be invoked.
type completedOptions struct {
	KeyVaultURI   string
	Config        *config.SecretSync
	ConfigFile    string
	PublicKeyFile string

	Secret     []byte
	SecretName string
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "config-file", name: "configuration file", value: &o.ConfigFile},
		{flag: "keyvault", name: "Azure KeyVault URI", value: &o.KeyVault},
		{flag: "secret-file", name: "secret file", value: &o.SecretFile},
		{flag: "secret-name", name: "secret name", value: &o.SecretName},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}

	validated, err := o.RawOptions.Validate()
	if err != nil {
		return nil, err
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions:       o,
			ValidatedOptions: validated,
		},
	}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	cfg, err := config.Load(o.ConfigFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	rawSecret, err := os.ReadFile(o.SecretFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret file %s: %w", o.SecretFile, err)
	}

	completed, err := o.ValidatedOptions.Complete()
	if err != nil {
		return nil, err
	}

	ev2Cloud := completed.Cloud
	if ev2Cloud == cmdutils.RolloutCloudDev {
		ev2Cloud = cmdutils.RolloutCloudPublic
	}

	region, err := ev2config.GetDefaultRegionForCloud(ev2Cloud)
	if err != nil {
		return nil, err
	}

	ev2Cfg, err := ev2config.ResolveConfig(string(ev2Cloud), region)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve ev2 config for %s: %w", ev2Cloud, err)
	}

	rawKeyVaultDNSSuffix, err := ev2Cfg.GetByPath("keyVault.domainNameSuffix")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve keyVault DNS suffix: %w", err)
	}

	keyVaultDNSSuffix, ok := rawKeyVaultDNSSuffix.(string)
	if !ok {
		return nil, fmt.Errorf("keyVault DNS suffix was %T, not string", rawKeyVaultDNSSuffix)
	}

	keyVaultURI := fmt.Sprintf("https://%s.%s", o.KeyVault, keyVaultDNSSuffix)

	keyVaultCfg, exists := cfg.KeyVaults[keyVaultURI]
	if !exists && o.PublicKeyFile == "" {
		return nil, fmt.Errorf("keyvault %s does not exist in encryption config and no public key file specified to bootstrap it", o.KeyVault)
	}

	if keyVaultCfg.KeyEncryptionKey == "" && o.PublicKeyFile == "" {
		return nil, fmt.Errorf("no public key recorded for key vault in encryption config and no public key file specified")
	}

	return &Options{
		completedOptions: &completedOptions{
			KeyVaultURI:   keyVaultURI,
			Config:        cfg,
			ConfigFile:    o.ConfigFile,
			PublicKeyFile: o.PublicKeyFile,
			SecretName:    o.SecretName,
			Secret:        rawSecret,
		},
	}, nil
}

func (opts *Options) Register(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	logger = logger.WithValues("keyvault", opts.KeyVaultURI)

	keyVault, exists := opts.Config.KeyVaults[opts.KeyVaultURI]
	if !exists {
		logger.Info("Adding KeyVault to encryption config.")
	}

	if keyVault.KeyEncryptionKey == "" {
		rawData, err := os.ReadFile(opts.PublicKeyFile)
		if err != nil {
			return fmt.Errorf("failed to read public key file %s: %w", opts.PublicKeyFile, err)
		}
		keyVault.KeyEncryptionKey = string(rawData)
		logger.Info("Updated public key.")
	}

	keyEncryptionBlock, _ := pem.Decode([]byte(keyVault.KeyEncryptionKey))
	if keyEncryptionBlock == nil {
		return fmt.Errorf("decoding public key yielded a nil block")
	}
	if keyEncryptionBlock.Type != "PUBLIC KEY" {
		return fmt.Errorf("decoding public key yielded a %s block, expected a PUBLIC KEY", keyEncryptionBlock.Type)
	}

	pkix, err := x509.ParsePKIXPublicKey(keyEncryptionBlock.Bytes)
	if err != nil {
		return fmt.Errorf("error while parsing public key: %w", err)
	}

	keyEncryptionKey, ok := pkix.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("expected an RSA public key, got %T", pkix)
	}

	dataEncryptionKey := make([]byte, 32)
	if _, err = rand.Read(dataEncryptionKey); err != nil {
		return fmt.Errorf("failed to read entropy when generating data encryption key: %w", err)
	}

	dataEncryptionBlock, err := aes.NewCipher(dataEncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to create AES cipher from data encryption key: %w", err)
	}

	aesgcm, err := cipher.NewGCMWithRandomNonce(dataEncryptionBlock)
	if err != nil {
		return fmt.Errorf("failed to create GCM cipher: %w", err)
	}

	secretCiphertext := aesgcm.Seal(nil, nil, opts.Secret, nil)
	encodedSecretCiphertext := make([]byte, base64.StdEncoding.EncodedLen(len(secretCiphertext)))
	base64.StdEncoding.Encode(encodedSecretCiphertext, secretCiphertext)

	dataEncryptionKeyCiphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, keyEncryptionKey, dataEncryptionKey, nil)
	if err != nil {
		return fmt.Errorf("error while encrypting data: %w", err)
	}

	encodedDataEncryptionKeyCiphertext := make([]byte, base64.StdEncoding.EncodedLen(len(dataEncryptionKeyCiphertext)))
	base64.StdEncoding.Encode(encodedDataEncryptionKeyCiphertext, dataEncryptionKeyCiphertext)

	if keyVault.EncryptedSecrets == nil {
		keyVault.EncryptedSecrets = map[string]config.EncryptedSecret{}
	}
	keyVault.EncryptedSecrets[opts.SecretName] = config.EncryptedSecret{
		DataEncryptionKey: string(encodedDataEncryptionKeyCiphertext),
		Data:              string(encodedSecretCiphertext),
	}

	if opts.Config.KeyVaults == nil {
		opts.Config.KeyVaults = map[string]config.KeyVault{}
	}
	opts.Config.KeyVaults[opts.KeyVaultURI] = keyVault

	rawCfg, err := yaml.Marshal(opts.Config)
	if err != nil {
		return fmt.Errorf("error while marshalling config: %w", err)
	}
	if err := os.WriteFile(opts.ConfigFile, rawCfg, 0666); err != nil {
		return fmt.Errorf("error while writing config: %w", err)
	}
	logger.Info("Updated encryption configuration.", "secret", opts.SecretName)
	return nil
}
