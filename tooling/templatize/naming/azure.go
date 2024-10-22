package naming

func AzureEventGridName(prefix string, suffixLength int, suffixDigestArgs ...string) (string, error) {
	return suffixedName(prefix, "-", 24, suffixLength, suffixDigestArgs...)
}

func AzurePostgresName(prefix string, suffixLength int, suffixDigestArgs ...string) (string, error) {
	return suffixedName(prefix, "-", 60, suffixLength, suffixDigestArgs...)
}

func AzureKeyVaultName(prefix string, suffixLength int, suffixDigestArgs ...string) (string, error) {
	return suffixedName(prefix, "-", 24, suffixLength, suffixDigestArgs...)
}
