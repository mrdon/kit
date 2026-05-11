// Command vault-migrate is a one-shot CLI that migrates a tenant's
// vault from the per-user-master-password model to the shared-master-
// password model. See plan: docs/plans/...
//
// Operation per tenant, in order:
//
//  1. Load the named user's row from app_vault_users.
//  2. Refuse if app_vault_tenants already has a row for this tenant.
//  3. Derive master_key from the old per-user password + the row's
//     kdf_params; verify it produces the row's stored auth_hash.
//  4. AES-GCM-decrypt the user's wrapped RSA private key with enc_key
//     derived from master_key.
//  5. RSA-OAEP-decrypt wrapped_vault_key to recover the raw vault_key.
//  6. Round-trip-decrypt one app_vault_entries row with vault_key to
//     prove we recovered the right key (skipable with --allow-empty-vault
//     for tenants that have no entries yet).
//  7. Derive a new master_key from the new shared password + a fresh
//     16-byte salt; produce new auth_hash and enc_key.
//  8. AES-GCM-wrap vault_key under the new enc_key with AAD = the raw
//     16 bytes of the tenant UUID.
//  9. INSERT into app_vault_tenants in a transaction; verify the
//     written row round-trips correctly before committing.
//
// Passwords are read from /dev/tty (terminal, no echo); they never
// appear in argv or environment. DATABASE_URL comes from the env, same
// as the kit binary.
//
// After running this for every tenant that needs migrating, run goose
// migration 051 to drop app_vault_users.
package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/term"
)

const (
	kdfIterations = 600_000
	kdfHash       = "pbkdf2-sha256"
	saltLen       = 16
	keyLen        = 32 // PBKDF2/HKDF output bytes
	vaultKeyLen   = 32 // AES-256 key
	gcmNonceLen   = 12
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

type flagsT struct {
	tenantID        uuid.UUID
	userID          uuid.UUID
	dryRun          bool
	allowEmptyVault bool
}

func parseFlags() (flagsT, error) {
	var (
		tenantIDFlag    string
		userIDFlag      string
		dryRun          bool
		allowEmptyVault bool
	)
	flag.StringVar(&tenantIDFlag, "tenant-id", "", "tenant UUID to migrate")
	flag.StringVar(&userIDFlag, "user-id", "", "user UUID whose vault_users row to read keys from (must know the old password)")
	flag.BoolVar(&dryRun, "dry-run", false, "compute everything but don't write app_vault_tenants")
	flag.BoolVar(&allowEmptyVault, "allow-empty-vault", false, "proceed even when the tenant has no app_vault_entries rows to round-trip against")
	flag.Parse()
	if tenantIDFlag == "" || userIDFlag == "" {
		flag.Usage()
		return flagsT{}, errors.New("--tenant-id and --user-id are required")
	}
	tID, err := uuid.Parse(tenantIDFlag)
	if err != nil {
		return flagsT{}, fmt.Errorf("--tenant-id: %w", err)
	}
	uID, err := uuid.Parse(userIDFlag)
	if err != nil {
		return flagsT{}, fmt.Errorf("--user-id: %w", err)
	}
	return flagsT{tenantID: tID, userID: uID, dryRun: dryRun, allowEmptyVault: allowEmptyVault}, nil
}

func run() error {
	f, err := parseFlags()
	if err != nil {
		return err
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connecting to db: %w", err)
	}
	defer pool.Close()

	if err := ensureNoExistingTenant(ctx, pool, f.tenantID); err != nil {
		return err
	}
	user, err := loadVaultUser(ctx, pool, f.tenantID, f.userID)
	if err != nil {
		return fmt.Errorf("loading vault_users row: %w", err)
	}
	oldPassword, newPassword, err := promptPasswords()
	if err != nil {
		return err
	}

	vaultKey, err := recoverVaultKey(oldPassword, user)
	if err != nil {
		return err
	}
	defer zeroBytes(vaultKey)
	if err := roundTripEntry(ctx, pool, f.tenantID, vaultKey, f.allowEmptyVault); err != nil {
		return fmt.Errorf("round-trip test: %w", err)
	}

	wrap, err := buildNewWrap(newPassword, vaultKey, f.tenantID)
	if err != nil {
		return err
	}

	if f.dryRun {
		printDryRun(f.tenantID, f.userID, wrap)
		return nil
	}
	if err := insertAndVerify(ctx, pool, f.tenantID, f.userID, vaultKey, wrap); err != nil {
		return err
	}

	fmt.Println("OK: vault migrated.")
	fmt.Println("Next steps:")
	fmt.Println("  1. Have every team member unlock the vault using the new shared password.")
	fmt.Println("  2. When you're confident the new vault is healthy, run goose migration 051")
	fmt.Println("     to drop app_vault_users.")
	return nil
}

// ===== phase helpers =====

func ensureNoExistingTenant(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) error {
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM app_vault_tenants WHERE tenant_id = $1)`,
		tenantID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("checking app_vault_tenants: %w", err)
	}
	if exists {
		return errors.New("app_vault_tenants already has a row for this tenant; refusing to clobber")
	}
	return nil
}

func promptPasswords() (oldPW, newPW string, err error) {
	oldPW, err = readPassword("Old per-user master password: ")
	if err != nil {
		return "", "", err
	}
	newPW, err = readPassword("New shared master password: ")
	if err != nil {
		return "", "", err
	}
	confirm, err := readPassword("Confirm new shared master password: ")
	if err != nil {
		return "", "", err
	}
	if newPW != confirm {
		return "", "", errors.New("new passwords do not match")
	}
	return oldPW, newPW, nil
}

// recoverVaultKey verifies the old password and unwraps the user's RSA
// private key, then RSA-OAEP-unwraps the tenant vault_key. Caller owns
// the returned bytes and must zeroBytes them when done.
func recoverVaultKey(oldPassword string, user *vaultUserRow) ([]byte, error) {
	salt, iters, err := parseKDFParams(user.kdfParams)
	if err != nil {
		return nil, fmt.Errorf("parsing old kdf_params: %w", err)
	}
	masterKey := pbkdf2.Key([]byte(oldPassword), salt, iters, keyLen, sha256.New)
	authHash, err := hkdfSHA256(masterKey, salt, []byte("kit-vault-v1-auth"), keyLen)
	if err != nil {
		return nil, fmt.Errorf("hkdf auth_hash: %w", err)
	}
	if subtle.ConstantTimeCompare(authHash, user.authHash) != 1 {
		return nil, errors.New("old password is wrong (auth_hash mismatch); no DB writes attempted")
	}
	encKey, err := hkdfSHA256(masterKey, salt, []byte("kit-vault-v1-enc"), keyLen)
	if err != nil {
		return nil, fmt.Errorf("hkdf enc_key: %w", err)
	}
	pkcs8, err := aesGcmDecrypt(encKey, user.userPrivateKeyNonce, user.userPrivateKeyCiphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting user private key: %w", err)
	}
	privAny, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		return nil, fmt.Errorf("parsing PKCS#8 private key: %w", err)
	}
	rsaPriv, ok := privAny.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	vaultKey, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, rsaPriv, user.wrappedVaultKey, nil)
	if err != nil {
		return nil, fmt.Errorf("RSA-OAEP unwrap: %w", err)
	}
	if len(vaultKey) != vaultKeyLen {
		return nil, fmt.Errorf("unwrapped vault_key has wrong length: %d (want %d)", len(vaultKey), vaultKeyLen)
	}
	return vaultKey, nil
}

type wrapResult struct {
	kdfParams       []byte
	authHash        []byte
	wrappedVaultKey []byte
	nonce           []byte
	encKeyForVerify []byte // kept so insertAndVerify can re-decrypt without re-deriving
	aad             []byte
}

func buildNewWrap(newPassword string, vaultKey []byte, tenantID uuid.UUID) (*wrapResult, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generating new salt: %w", err)
	}
	masterKey := pbkdf2.Key([]byte(newPassword), salt, kdfIterations, keyLen, sha256.New)
	authHash, err := hkdfSHA256(masterKey, salt, []byte("kit-vault-v1-auth"), keyLen)
	if err != nil {
		return nil, fmt.Errorf("new hkdf auth_hash: %w", err)
	}
	encKey, err := hkdfSHA256(masterKey, salt, []byte("kit-vault-v1-enc"), keyLen)
	if err != nil {
		return nil, fmt.Errorf("new hkdf enc_key: %w", err)
	}
	aad := tenantID[:]
	nonce := make([]byte, gcmNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating new nonce: %w", err)
	}
	wrapped, err := aesGcmEncrypt(encKey, nonce, vaultKey, aad)
	if err != nil {
		return nil, fmt.Errorf("AES-GCM wrap: %w", err)
	}
	// Round-trip locally before writing.
	rt, err := aesGcmDecrypt(encKey, nonce, wrapped, aad)
	if err != nil {
		return nil, fmt.Errorf("post-wrap round-trip: %w", err)
	}
	if subtle.ConstantTimeCompare(rt, vaultKey) != 1 {
		return nil, errors.New("post-wrap round-trip: vault_key bytes differ")
	}
	zeroBytes(rt)
	kdfJSON, err := json.Marshal(struct {
		Algo       string `json:"algo"`
		Iterations int    `json:"iterations"`
		Salt       string `json:"salt"`
	}{Algo: kdfHash, Iterations: kdfIterations, Salt: base64.StdEncoding.EncodeToString(salt)})
	if err != nil {
		return nil, fmt.Errorf("marshalling new kdf_params: %w", err)
	}
	return &wrapResult{
		kdfParams:       kdfJSON,
		authHash:        authHash,
		wrappedVaultKey: wrapped,
		nonce:           nonce,
		encKeyForVerify: encKey,
		aad:             aad,
	}, nil
}

func printDryRun(tenantID, userID uuid.UUID, w *wrapResult) {
	fmt.Println("--dry-run: would insert into app_vault_tenants:")
	fmt.Printf("  tenant_id               = %s\n", tenantID)
	fmt.Printf("  kdf_params              = %s\n", string(w.kdfParams))
	fmt.Printf("  auth_hash               = %s (hex)\n", hex.EncodeToString(w.authHash))
	fmt.Printf("  wrapped_vault_key       = %s (hex)\n", hex.EncodeToString(w.wrappedVaultKey))
	fmt.Printf("  wrapped_vault_key_nonce = %s (hex)\n", hex.EncodeToString(w.nonce))
	fmt.Printf("  vault_generation        = 1\n")
	fmt.Printf("  last_rotated_by_user_id = %s\n", userID)
}

func insertAndVerify(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, vaultKey []byte, w *wrapResult) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO app_vault_tenants
			(tenant_id, kdf_params, auth_hash,
			 wrapped_vault_key, wrapped_vault_key_nonce,
			 vault_generation, last_rotated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, 1, $6)
	`, tenantID, w.kdfParams, w.authHash, w.wrappedVaultKey, w.nonce, userID); err != nil {
		return fmt.Errorf("inserting app_vault_tenants: %w", err)
	}

	var verifyKDF, verifyAuthHash, verifyWrapped, verifyNonce []byte
	if err := tx.QueryRow(ctx, `
		SELECT kdf_params, auth_hash, wrapped_vault_key, wrapped_vault_key_nonce
		FROM app_vault_tenants WHERE tenant_id = $1
	`, tenantID).Scan(&verifyKDF, &verifyAuthHash, &verifyWrapped, &verifyNonce); err != nil {
		return fmt.Errorf("re-reading inserted row: %w", err)
	}
	if subtle.ConstantTimeCompare(verifyAuthHash, w.authHash) != 1 {
		return errors.New("post-insert verify: auth_hash differs — refusing to commit")
	}
	verifiedVK, err := aesGcmDecrypt(w.encKeyForVerify, verifyNonce, verifyWrapped, w.aad)
	if err != nil {
		return fmt.Errorf("post-insert verify: AES-GCM decrypt: %w", err)
	}
	if subtle.ConstantTimeCompare(verifiedVK, vaultKey) != 1 {
		return errors.New("post-insert verify: vault_key differs — refusing to commit")
	}
	zeroBytes(verifiedVK)

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}

// ===== DB =====

type vaultUserRow struct {
	kdfParams                json.RawMessage
	authHash                 []byte
	userPrivateKeyCiphertext []byte
	userPrivateKeyNonce      []byte
	wrappedVaultKey          []byte
}

func loadVaultUser(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) (*vaultUserRow, error) {
	var v vaultUserRow
	err := pool.QueryRow(ctx, `
		SELECT kdf_params, auth_hash,
		       user_private_key_ciphertext, user_private_key_nonce,
		       wrapped_vault_key
		FROM app_vault_users
		WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, userID).Scan(
		&v.kdfParams, &v.authHash,
		&v.userPrivateKeyCiphertext, &v.userPrivateKeyNonce,
		&v.wrappedVaultKey,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("no app_vault_users row for (tenant_id, user_id)")
		}
		return nil, err
	}
	if v.wrappedVaultKey == nil {
		return nil, errors.New("user has no wrapped_vault_key (not granted yet); pick a different --user-id")
	}
	return &v, nil
}

// roundTripEntry decrypts one app_vault_entries row to prove we
// recovered the right vault_key. Skips with a warning if the tenant
// has zero entries (and --allow-empty-vault was set).
func roundTripEntry(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, vaultKey []byte, allowEmpty bool) error {
	var ct, nonce []byte
	err := pool.QueryRow(ctx, `
		SELECT value_ciphertext, value_nonce FROM app_vault_entries
		WHERE tenant_id = $1
		LIMIT 1
	`, tenantID).Scan(&ct, &nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		if allowEmpty {
			fmt.Fprintln(os.Stderr, "warning: tenant has no app_vault_entries to round-trip against; --allow-empty-vault is set, proceeding anyway")
			return nil
		}
		return errors.New("tenant has no app_vault_entries to round-trip against; pass --allow-empty-vault to bypass this check (use at your own risk)")
	}
	if err != nil {
		return err
	}
	plaintext, err := aesGcmDecrypt(vaultKey, nonce, ct, nil)
	if err != nil {
		return fmt.Errorf("round-trip AES-GCM decrypt: %w", err)
	}
	// Sanity-check the JSON shape — entries are
	// {"password":"...","notes":"...","totp":{...}?}
	var probe struct{}
	if err := json.Unmarshal(plaintext, &probe); err != nil {
		return fmt.Errorf("round-trip decrypted blob is not JSON: %w", err)
	}
	zeroBytes(plaintext)
	fmt.Fprintln(os.Stderr, "round-trip OK: existing entry decrypted with recovered vault_key")
	return nil
}

// ===== crypto helpers =====

func parseKDFParams(raw json.RawMessage) (salt []byte, iterations int, err error) {
	var p struct {
		Algo       string `json:"algo"`
		Iterations int    `json:"iterations"`
		Salt       string `json:"salt"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, 0, err
	}
	if p.Algo != "pbkdf2-sha256" {
		return nil, 0, fmt.Errorf("unsupported KDF algo: %q (only pbkdf2-sha256)", p.Algo)
	}
	if p.Iterations < 1000 {
		return nil, 0, fmt.Errorf("KDF iterations too low: %d", p.Iterations)
	}
	salt, err = base64.StdEncoding.DecodeString(p.Salt)
	if err != nil {
		return nil, 0, fmt.Errorf("decoding salt: %w", err)
	}
	return salt, p.Iterations, nil
}

func hkdfSHA256(masterKey, salt, info []byte, n int) ([]byte, error) {
	r := hkdf.New(sha256.New, masterKey, salt, info)
	out := make([]byte, n)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}

func aesGcmEncrypt(key, nonce, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return g.Seal(nil, nonce, plaintext, aad), nil
}

func aesGcmDecrypt(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return g.Open(nil, nonce, ciphertext, aad)
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// readPassword reads a password from /dev/tty (or stdin if /dev/tty isn't
// available) without echoing. Refuses to read from a non-terminal so
// scripts can't accidentally pipe a password in clear text.
func readPassword(prompt string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("opening /dev/tty: %w (run interactively, not piped)", err)
	}
	defer tty.Close()
	fmt.Fprint(tty, prompt)
	bytes, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

var _ = hmac.Equal // keep crypto/hmac import; available for future MAC verification needs
