package naming

func AzureEventGridName(prefix string, suffixArgs ...string) string {
	// todo other naming rules
	return SuffixedName(prefix, "-", 24, suffixArgs...)
}

func AzurePostgresName(prefix string, suffixArgs ...string) string {
	// todo other naming rules
	return SuffixedName(prefix, "-", 60, suffixArgs...)
}

func AzureKeyVaultName(prefix string, suffixArgs ...string) string {
	// todo other naming rules
	return SuffixedName(prefix, "-", 24, suffixArgs...)
}
