package populate

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/pkg/cmdutils"
	"github.com/Azure/ARO-Tools/pkg/config/ev2config"
	"github.com/Azure/ARO-Tools/pkg/secret-sync/config"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		RawOptions: &cmdutils.RawOptions{},
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ConfigFile, "config-file", opts.ConfigFile, "Configuration for what to register, encoded as a JSON string.")
	cmd.Flags().StringVar(&opts.KeyVault, "keyvault", opts.KeyVault, "KeyVault URI into which we will populate secrets.")
	cmd.Flags().StringVar(&opts.KeyEncryptionKey, "key-encryption-key", opts.KeyEncryptionKey, "Name of key encryption key in KeyVault.")
	for _, flag := range []string{"config-file"} {
		if err := cmd.MarkFlagFilename(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	return cmdutils.BindOptions(opts.RawOptions, cmd)
}

// RawOptions holds input values.
type RawOptions struct {
	*cmdutils.RawOptions
	KeyVault         string
	KeyEncryptionKey string
	ConfigFile       string
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
	KeyVaultURI      string
	Config           config.KeyVault
	KeyEncryptionKey string

	SecretsClient *azsecrets.Client
	KeysClient    *azkeys.Client
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
		{flag: "key-encryption-key", name: "Azure KeyVault key encryption key name", value: &o.KeyEncryptionKey},
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

	completed, err := o.ValidatedOptions.Complete()
	if err != nil {
		return nil, err
	}

	ev2Cloud := completed.Cloud
	if ev2Cloud == cmdutils.RolloutCloudDev {
		ev2Cloud = cmdutils.RolloutCloudPublic
	}

	ev2Cfg, err := ev2config.ResolveConfig(string(ev2Cloud), "uksouth") // n.b. region is not important
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
	if !exists {
		return nil, fmt.Errorf("keyvault %s does not exist in encryption config", keyVaultURI)
	}

	creds, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credentials: %w", err)
	}

	clientOpts := azcore.ClientOptions{
		Cloud: completed.Configuration,
	}

	secretsClient, err := azsecrets.NewClient(keyVaultURI, creds, &azsecrets.ClientOptions{ClientOptions: clientOpts})
	if err != nil {
		return nil, fmt.Errorf("failed to create secrets client: %w", err)
	}
	keysClient, err := azkeys.NewClient(keyVaultURI, creds, &azkeys.ClientOptions{ClientOptions: clientOpts})
	if err != nil {
		return nil, fmt.Errorf("failed to create keys client: %w", err)
	}

	return &Options{
		completedOptions: &completedOptions{
			KeyVaultURI:      keyVaultURI,
			Config:           keyVaultCfg,
			KeyEncryptionKey: o.KeyEncryptionKey,
			SecretsClient:    secretsClient,
			KeysClient:       keysClient,
		},
	}, nil
}

func (opts *Options) Populate(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	logger = logger.WithValues("keyvault", opts.KeyVaultURI, "encryptionKey", opts.KeyEncryptionKey)
	logger.Info("Populating secrets.")

	for name, secret := range opts.Config.EncryptedSecrets {
		secretLogger := logger.WithValues("secret", name)

		secretLogger.Info("Decrypting data encryption key.")
		secretCiphertext, err := base64.StdEncoding.DecodeString(secret.Data)
		if err != nil {
			return fmt.Errorf("failed to decode raw data: %w", err)
		}

		dataEncryptionKeyCiphertext, err := base64.StdEncoding.DecodeString(secret.DataEncryptionKey)
		if err != nil {
			return fmt.Errorf("failed to decode raw data: %w", err)
		}

		dataEncryptionKey, err := opts.KeysClient.Decrypt(ctx, opts.KeyEncryptionKey, "",
			azkeys.KeyOperationParameters{
				Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP256),
				Value:     dataEncryptionKeyCiphertext,
			},
			&azkeys.DecryptOptions{},
		)
		if err != nil {
			return fmt.Errorf("failed to decrypt data encryption key %s: %w", name, err)
		}

		secretLogger.Info("Decrypting secret data.")

		dataEncryptionBlock, err := aes.NewCipher(dataEncryptionKey.Result)
		if err != nil {
			return fmt.Errorf("failed to create AES cipher from data encryption key: %w", err)
		}

		aesgcm, err := cipher.NewGCMWithRandomNonce(dataEncryptionBlock)
		if err != nil {
			return fmt.Errorf("failed to create GCM cipher: %w", err)
		}

		secretPlaintext, err := aesgcm.Open(nil, nil, secretCiphertext, nil)
		if err != nil {
			return fmt.Errorf("failed to decrypt data: %w", err)
		}

		currentSecret, err := opts.SecretsClient.GetSecret(ctx, name, "", nil)
		var respErr *azcore.ResponseError
		if err != nil && errors.As(err, &respErr) && respErr.StatusCode != http.StatusNotFound {
			return fmt.Errorf("failed to get secret %s: %w", name, err)
		}

		if currentSecret.Value != nil && *currentSecret.Value == string(secretPlaintext) {
			secretLogger.Info("Secret value up-to-date.")
			continue
		}

		secretLogger.Info("Populating secret.")
		if _, err := opts.SecretsClient.SetSecret(ctx, name, azsecrets.SetSecretParameters{
			Value: to.Ptr(string(secretPlaintext)),
		}, nil); err != nil {
			return fmt.Errorf("failed to set secret %s: %w", name, err)
		}

		secretLogger.Info("Populated secret.")
	}

	return nil
}
