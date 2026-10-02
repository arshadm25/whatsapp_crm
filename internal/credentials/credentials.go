// Package credentials opens the Meta access tokens stored envelope-encrypted in meta_credentials.
// The plain token lives only in memory for the call that needs it.
package credentials

import (
	"context"

	"github.com/google/uuid"

	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

// AAD binds a stored token to its tenant and WhatsApp account, so a ciphertext copied to
// another row does not decrypt.
func AAD(tenantID, accountID uuid.UUID) []byte {
	return []byte("meta_credentials:" + tenantID.String() + ":" + accountID.String())
}

// Token decrypts the account's active Business Integration System User token and records its use.
// q must run in the account's tenant.
func Token(ctx context.Context, q *dbq.Queries, keys *envelope.Keyring, acct dbq.WhatsappAccount) (string, error) {
	cred, err := q.GetActiveCredential(ctx, acct.ID)
	if err != nil {
		return "", err
	}
	pt, err := keys.Open(envelope.Sealed{
		Ciphertext: cred.TokenCiphertext, DataKeyEncrypted: cred.DataKeyCiphertext, MasterKeyVersion: int(cred.MasterKeyVersion),
	}, AAD(acct.TenantID, acct.ID))
	if err != nil {
		return "", err
	}
	if err := q.TouchCredential(ctx, cred.ID); err != nil {
		return "", err
	}
	return string(pt), nil
}
