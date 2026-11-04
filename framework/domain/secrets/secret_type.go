package secrets

type SecretType string

const (
	SecretReusable  SecretType = "reusable"
	SecretTransient SecretType = "transient"
)
