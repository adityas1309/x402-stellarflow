package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/aead/chacha20poly1305"
	"github.com/google/uuid"
	"github.com/o1egl/paseto"
)

// Different types of error returned by the VerifyToken function
var (
	ErrInvalidToken = errors.New("token is invalid")
	ErrExpiredToken = errors.New("token has expired")
)

// Payload contains the payload data of the token
type Payload struct {
	ID           uuid.UUID `json:"id"`
	UserID       int64     `json:"user_id"`
	OrgID        int64     `json:"org_id"`
	Email        string    `json:"email"`
	Role         string    `json:"role"`
	IsSuperAdmin bool      `json:"is_super_admin"`
	Scope        string    `json:"scope"`
	IssuedAt     time.Time `json:"issued_at"`
	ExpiredAt    time.Time `json:"expired_at"`
}

// NewPayload creates a new token payload with a specific email and duration
func NewPayload(email string, userID int64, orgID int64, role string, isSuperAdmin bool, duration time.Duration, isAccessToken bool) (*Payload, error) {
	tokenID, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}

	scope := "access_token"
	if !isAccessToken {
		scope = "refresh_token"
	}

	payload := &Payload{
		ID:           tokenID,
		UserID:       userID,
		OrgID:        orgID,
		Email:        email,
		Role:         role,
		IsSuperAdmin: isSuperAdmin,
		Scope:        scope,
		IssuedAt:     time.Now(),
		ExpiredAt:    time.Now().Add(duration),
	}
	return payload, nil
}

// Valid checks if the token payload is valid or not
func (payload *Payload) Valid() error {
	if time.Now().After(payload.ExpiredAt) {
		return ErrExpiredToken
	}
	return nil
}

// Maker is an interface for managing tokens
type Maker interface {
	CreateToken(email string, userID int64, orgID int64, role string, isSuperAdmin bool, duration time.Duration, isAccessToken bool) (string, *Payload, error)
	VerifyToken(token string) (*Payload, error)
}

// PasetoMaker is a PASETO token maker
type PasetoMaker struct {
	paseto       *paseto.V2
	symmetricKey []byte
}

// NewPasetoMaker creates a new PasetoMaker
func NewPasetoMaker(symmetricKey string) (Maker, error) {
	if len(symmetricKey) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("invalid key size: must be exactly %d characters", chacha20poly1305.KeySize)
	}

	maker := &PasetoMaker{
		paseto:       paseto.NewV2(),
		symmetricKey: []byte(symmetricKey),
	}

	return maker, nil
}

// CreateToken creates a new token for a specific user and duration
func (maker *PasetoMaker) CreateToken(email string, userID int64, orgID int64, role string, isSuperAdmin bool, duration time.Duration, isAccessToken bool) (string, *Payload, error) {
	payload, err := NewPayload(email, userID, orgID, role, isSuperAdmin, duration, isAccessToken)
	if err != nil {
		return "", payload, err
	}

	token, err := maker.paseto.Encrypt(maker.symmetricKey, payload, nil)
	return token, payload, err
}

// VerifyToken checks if the token is valid or not
func (maker *PasetoMaker) VerifyToken(token string) (*Payload, error) {
	payload := &Payload{}

	err := maker.paseto.Decrypt(token, maker.symmetricKey, payload, nil)
	if err != nil {
		return nil, ErrInvalidToken
	}

	err = payload.Valid()
	if err != nil {
		return nil, err
	}

	return payload, nil
}
