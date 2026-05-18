package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTConfig 持有 JWT 签名与校验所需的参数。
type JWTConfig struct {
	Secret string
	Issuer string
	TTL    time.Duration
}

// JWTManager 用 HS256 签 / 验 JWT token。
//
// 安全提示：当 Issuer 为空字符串时，ParseToken **不**校验 iss claim。
// 这意味着任何持有相同 secret 但用不同 iss 颁发的 token 都能通过本 manager
// 验证。生产环境务必显式配置非空 Issuer；骨架在 config/validate.go 里强制了
// JWT_ISSUER 在 JWT_SECRET 非空时必填，作为运维错配防御。
//
// now 字段允许测试注入虚拟时钟（默认 time.Now），便于覆盖 exp / nbf 边界。
type JWTManager struct {
	secret []byte
	issuer string
	ttl    time.Duration
	now    func() time.Time
}

// Claims 是骨架统一用的 JWT 载荷类型，只内嵌 RegisteredClaims，未来想加业
// 务字段（roles / tenant_id 等）在这里扩展。
type Claims struct {
	jwt.RegisteredClaims
}

var (
	// ErrMissingSecret 在 JWTConfig.Secret 为空时返回。
	ErrMissingSecret = errors.New("jwt secret is required")
	// ErrMissingSubject 在 token subject 为空时返回（签发 / 校验都会触发）。
	ErrMissingSubject = errors.New("jwt subject is required")
)

// NewJWTManager 用 cfg 构造 JWTManager。Secret 为空时直接拒绝，避免运行期才
// 发现没配密钥。
func NewJWTManager(cfg JWTConfig) (*JWTManager, error) {
	if strings.TrimSpace(cfg.Secret) == "" {
		return nil, ErrMissingSecret
	}
	return &JWTManager{
		secret: []byte(cfg.Secret),
		issuer: strings.TrimSpace(cfg.Issuer),
		ttl:    cfg.TTL,
		now:    time.Now,
	}, nil
}

// GenerateToken 给 subject 签一个 token，自动填 iat / nbf；ttl > 0 时
// 顺带填 exp，让 token 过期失效。
func (m *JWTManager) GenerateToken(subject string) (string, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", ErrMissingSubject
	}

	now := m.now().UTC()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    m.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	if m.ttl > 0 {
		claims.ExpiresAt = jwt.NewNumericDate(now.Add(m.ttl))
	}

	return m.GenerateTokenWithClaims(claims)
}

// GenerateTokenWithClaims 用 caller 提供的完整 claims 签 token，便于把业务
// 自定义字段塞进去。Issuer 为空时回填 manager 默认 issuer。
func (m *JWTManager) GenerateTokenWithClaims(claims Claims) (string, error) {
	if strings.TrimSpace(claims.Subject) == "" {
		return "", ErrMissingSubject
	}
	if claims.Issuer == "" {
		claims.Issuer = m.issuer
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign jwt token: %w", err)
	}
	return signed, nil
}

// ParseToken 校验 Bearer 或裸 HS256 JWT token。校验项：签名算法只允许
// HS256（防 alg=none 攻击）、exp / nbf（基于 manager.now 注入的时钟）、
// 配置了 Issuer 时校验 iss。subject 为空也算 invalid。
func (m *JWTManager) ParseToken(tokenString string) (*Claims, error) {
	tokenString = normalizeBearerToken(tokenString)
	if tokenString == "" {
		return nil, errors.New("jwt token is required")
	}

	options := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(func() time.Time {
			return m.now().UTC()
		}),
	}
	if m.issuer != "" {
		options = append(options, jwt.WithIssuer(m.issuer))
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		return m.secret, nil
	}, options...)
	if err != nil {
		return nil, fmt.Errorf("parse jwt token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid jwt token")
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return nil, ErrMissingSubject
	}
	return claims, nil
}

// normalizeBearerToken 把 "Bearer xxx" 这种 Authorization 头格式剥掉前缀，
// 让调用方既可以传整段头，也可以传纯 token。前缀大小写不敏感。
func normalizeBearerToken(token string) string {
	token = strings.TrimSpace(token)
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		return strings.TrimSpace(token[len("bearer "):])
	}
	return token
}
