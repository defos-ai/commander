package secrets

type SecretType string

const MaxTransientSecretBytes = 64 * 1024

const (
	SecretReusable  SecretType = "reusable"
	SecretTransient SecretType = "transient"
)
