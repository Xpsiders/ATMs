package testutil

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"attendance-api/internal/auth"
)

type refreshRecord struct {
	identity  auth.Identity
	expiresAt time.Time
	revoked   bool
}

type MemoryRefreshStore struct {
	mu      sync.Mutex
	records map[string]refreshRecord
}

func NewMemoryRefreshStore() *MemoryRefreshStore {
	return &MemoryRefreshStore{records: make(map[string]refreshRecord)}
}

func (s *MemoryRefreshStore) Create(ctx context.Context, hash []byte, identity auth.Identity, expiresAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hex.EncodeToString(hash)
	if _, exists := s.records[key]; exists {
		return errors.New("refresh token already exists")
	}
	s.records[key] = refreshRecord{identity: identity, expiresAt: expiresAt}
	return nil
}

func (s *MemoryRefreshStore) Rotate(ctx context.Context, oldHash, newHash []byte, now, expiresAt time.Time) (auth.Identity, error) {
	if err := ctx.Err(); err != nil {
		return auth.Identity{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	oldKey := hex.EncodeToString(oldHash)
	record, exists := s.records[oldKey]
	if !exists || record.revoked || !now.Before(record.expiresAt) {
		return auth.Identity{}, auth.ErrInvalidRefresh
	}
	newKey := hex.EncodeToString(newHash)
	if _, exists := s.records[newKey]; exists {
		return auth.Identity{}, errors.New("replacement refresh token already exists")
	}
	record.revoked = true
	s.records[oldKey] = record
	s.records[newKey] = refreshRecord{identity: record.identity, expiresAt: expiresAt}
	return record.identity, nil
}

func (s *MemoryRefreshStore) Revoke(ctx context.Context, hash []byte, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hex.EncodeToString(hash)
	record, exists := s.records[key]
	if !exists || record.revoked || !now.Before(record.expiresAt) {
		return auth.ErrInvalidRefresh
	}
	record.revoked = true
	s.records[key] = record
	return nil
}
