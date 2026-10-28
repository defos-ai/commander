package fixtures

// Fixed AES-GCM ciphertext with literal v3 authenticated data guards the persisted format.
const PersistedV3Envelope = `{
		"metadata": {
			"id": "00000000-0000-4000-8000-000000000001",
			"key": {"name": "API_TOKEN"},
			"version": 2,
			"created_at": "2026-10-16T11:00:00Z",
			"updated_at": "2026-10-16T12:00:00Z",
			"expires_at": "2026-10-16T13:00:00Z"
		},
		"value": {
			"format": "aes-256-gcm-v3",
			"key_id": "sha256-4884fdaafea47c29fea7159d0daddd9c",
			"nonce": "AAECAwQFBgcICQoL",
			"ciphertext": "8WHobm+UVm3+678gV8zHozqpYJ69mVJp3ajiBZK2WxvVnKOaf/5SrkgkOg=="
		}
	}`
