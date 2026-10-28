package fixtures

import (
	"fmt"
)

func PrivateRelease(tag string) string {
	return fmt.Sprintf(`{"tag_name":%q,"draft":false,"prerelease":false,"published_at":"2026-10-01T12:00:00Z","assets":[
		{"id":11,"name":"agent.yaml","url":"https://untrusted.invalid/manifest"},
		{"id":12,"name":%q,"url":"https://untrusted.invalid/binary"}]}`, tag, BinaryAsset)
}
