package sso

import (
	"context"
	"encoding/json"
	"errors"
)

func (s *OIDCService) storeAuthRequest(ctx context.Context, code string, ar authRequest) error {
	body, err := json.Marshal(ar)
	if err != nil {
		return err
	}
	return s.redis.Set(ctx, authReqRedisPrefix+code, string(body), authReqTTL)
}

func (s *OIDCService) consumeAuthRequest(ctx context.Context, code string) (*authRequest, error) {
	key := authReqRedisPrefix + code
	raw, err := s.redis.GetString(ctx, key)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, errors.New("auth code unknown or expired")
	}
	var ar authRequest
	if err := json.Unmarshal([]byte(raw), &ar); err != nil {
		return nil, err
	}
	_, _ = s.redis.DeleteKey(ctx, key)
	return &ar, nil
}

func (s *OIDCService) storeRefresh(ctx context.Context, rt string, rec refreshRecord) error {
	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.redis.Set(ctx, refreshRedisPrefix+rt, string(body), refreshTokenTTL)
}

func (s *OIDCService) loadRefresh(ctx context.Context, rt string) (*refreshRecord, error) {
	raw, err := s.redis.GetString(ctx, refreshRedisPrefix+rt)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, errors.New("refresh token unknown or expired")
	}
	var rec refreshRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}
