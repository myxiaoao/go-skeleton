package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTManagerGenerateAndParse(t *testing.T) {
	manager, err := NewJWTManager(JWTConfig{
		Secret: "test-secret",
		Issuer: "go-skeleton",
		TTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}
	manager.now = func() time.Time {
		return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	}

	token, err := manager.GenerateToken("account-1")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	claims, err := manager.ParseToken("Bearer " + token)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.Subject != "account-1" {
		t.Fatalf("Subject = %q, want account-1", claims.Subject)
	}
	if claims.Issuer != "go-skeleton" {
		t.Fatalf("Issuer = %q, want go-skeleton", claims.Issuer)
	}
}

func TestJWTManagerValidation(t *testing.T) {
	_, err := NewJWTManager(JWTConfig{})
	if !errors.Is(err, ErrMissingSecret) {
		t.Fatalf("expected ErrMissingSecret, got %v", err)
	}

	manager, err := NewJWTManager(JWTConfig{Secret: "test-secret"})
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}
	if _, err := manager.GenerateToken(" "); !errors.Is(err, ErrMissingSubject) {
		t.Fatalf("expected ErrMissingSubject, got %v", err)
	}
}

func TestJWTManagerRejectsWrongIssuer(t *testing.T) {
	signer, err := NewJWTManager(JWTConfig{Secret: "test-secret", Issuer: "issuer-a", TTL: time.Hour})
	if err != nil {
		t.Fatalf("NewJWTManager signer: %v", err)
	}
	verifier, err := NewJWTManager(JWTConfig{Secret: "test-secret", Issuer: "issuer-b", TTL: time.Hour})
	if err != nil {
		t.Fatalf("NewJWTManager verifier: %v", err)
	}

	token, err := signer.GenerateToken("account-1")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	if _, err := verifier.ParseToken(token); err == nil {
		t.Fatal("expected issuer validation error")
	}
}

func TestJWTManagerRejectsNonHS256(t *testing.T) {
	manager, err := NewJWTManager(JWTConfig{Secret: "test-secret"})
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "account-1"},
	})
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}

	if _, err := manager.ParseToken(signed); err == nil {
		t.Fatal("expected signing method validation error")
	}
}

// TestJWTManagerRejectsTokenWithoutExp 保证 ParseToken 显式启用了
// WithExpirationRequired()：哪怕签名和 issuer 都对，缺 exp 也必须被拒。
// 防止 jwt/v5 默认 "exp optional" 让永不过期 token 蒙混过关。
func TestJWTManagerRejectsTokenWithoutExp(t *testing.T) {
	manager, err := NewJWTManager(JWTConfig{
		Secret: "test-secret",
		Issuer: "go-skeleton",
		TTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	// 绕过 GenerateToken 的本地保护，手工签一个无 exp 的合法 HS256 token，
	// 模拟攻击者拿到 secret 后签 long-lived token 的场景。
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "account-1",
			Issuer:  "go-skeleton",
		},
	})
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}

	if _, err := manager.ParseToken(signed); err == nil {
		t.Fatal("expected exp-required validation error")
	}
}

// TestJWTManagerRejectsExpiredToken 显式覆盖 exp 过期路径：基于注入时钟
// 把 token 签在过去再用当下时间校验，确保 WithExpirationRequired() 没把
// 过期判断也一起改坏。
func TestJWTManagerRejectsExpiredToken(t *testing.T) {
	manager, err := NewJWTManager(JWTConfig{
		Secret: "test-secret",
		Issuer: "go-skeleton",
		TTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}
	manager.now = func() time.Time {
		return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	}
	token, err := manager.GenerateToken("account-1")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// 时钟跳到 ttl 之后。
	manager.now = func() time.Time {
		return time.Date(2026, 5, 15, 14, 0, 0, 0, time.UTC)
	}
	if _, err := manager.ParseToken(token); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

// TestJWTManagerRejectsZeroTTL 覆盖 GenerateToken 在 ttl<=0 时的 fail-fast：
// 没有 TTL 就不应该让 caller 拿到一个自己 ParseToken 都验不过的 token。
func TestJWTManagerRejectsZeroTTL(t *testing.T) {
	manager, err := NewJWTManager(JWTConfig{Secret: "test-secret"})
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}
	if _, err := manager.GenerateToken("account-1"); !errors.Is(err, ErrMissingTTL) {
		t.Fatalf("expected ErrMissingTTL, got %v", err)
	}

	// GenerateTokenWithClaims 也要拒：caller 自己手工拼 claims 但没填 exp。
	if _, err := manager.GenerateTokenWithClaims(Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "account-1"},
	}); !errors.Is(err, ErrMissingTTL) {
		t.Fatalf("GenerateTokenWithClaims expected ErrMissingTTL, got %v", err)
	}
}
