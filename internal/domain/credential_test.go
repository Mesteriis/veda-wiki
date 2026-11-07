package domain

import "testing"

func TestCredentialLikeIgnoresReferencesAndPlaceholders(t *testing.T) {
	for _, text := range []string{
		"    api_key: !secret herald_omniroute_api_key",
		"password: !env_var DB_PASSWORD",
		"- Support API-key identity `apikey::<me.id>::<sha256(key)>` and CPA conversion.",
		"access_token=${GITHUB_TOKEN}",
		"client_secret: {{ vault_client_secret }}",
		"password = %(db_password)s",
		"api-key: $OMNIROUTE_KEY",
		"api_key: <your-api-key>",
		"The password field is required.",
	} {
		if CredentialLike(text) || CredentialLikeBytes([]byte(text)) {
			t.Errorf("flagged a reference or placeholder: %q", text)
		}
	}
	for _, text := range []string{
		"password=synthetic-fixture-only",
		"api_key: sk1234567890abcdef",
		"access_token: \"abc123def456\"",
		"api-key: $lowercase-literal",
		"token ghp_abcdefghijklmnopqrstuvwx",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"https://user:pass@example.com/repo",
		// A reference elsewhere does not hide a literal secret.
		"api_key: !secret name\npassword: hunter2-literal",
	} {
		if !CredentialLike(text) || !CredentialLikeBytes([]byte(text)) {
			t.Errorf("missed credential-like text: %q", text)
		}
	}
}
