package blob

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"aegis/platform/consts"
	"aegis/platform/duckdbquery"
)

// queryPresignTTL covers schema describe + count(*) + the user query
// against the same VIEWs within one request.
const queryPresignTTL = 15 * time.Minute

// maxQuerySources caps how many parquet files one query may span, so a
// broad prefix can't fan out into thousands of presigns + VIEWs.
const maxQuerySources = 512

// resolveQuerySources turns a {prefix | keys} selector into a set of
// duckdbquery.Source — one VIEW per *.parquet object, each backed by a
// freshly minted presigned GET URL. keys takes precedence over prefix
// when both are given. Non-parquet keys under a prefix are skipped;
// explicit non-parquet keys are an error.
func (s *Service) resolveQuerySources(ctx context.Context, bucket, prefix string, keys []string) ([]duckdbquery.Source, error) {
	objKeys, err := s.collectParquetKeys(ctx, bucket, prefix, keys)
	if err != nil {
		return nil, err
	}
	if len(objKeys) == 0 {
		return nil, fmt.Errorf("%w: no parquet objects matched", consts.ErrNotFound)
	}
	if len(objKeys) > maxQuerySources {
		return nil, fmt.Errorf("%w: %d parquet objects exceed the %d-source limit; narrow the prefix or pass explicit keys",
			consts.ErrBadRequest, len(objKeys), maxQuerySources)
	}
	sources := make([]duckdbquery.Source, 0, len(objKeys))
	seen := make(map[string]struct{}, len(objKeys))
	for _, key := range objKeys {
		view := duckdbquery.SanitizeViewName(strings.TrimSuffix(path.Base(key), path.Ext(key)))
		if view == "" {
			continue
		}
		// Disambiguate colliding filestems (a/x.parquet vs b/x.parquet).
		base := view
		for i := 2; ; i++ {
			if _, ok := seen[view]; !ok {
				break
			}
			view = fmt.Sprintf("%s_%d", base, i)
		}
		seen[view] = struct{}{}

		pr, err := s.PresignGet(ctx, bucket, key, GetOpts{TTL: queryPresignTTL})
		if err != nil {
			return nil, fmt.Errorf("presign %q: %w", key, err)
		}
		sources = append(sources, duckdbquery.Source{View: view, URL: pr.URL})
	}
	return sources, nil
}

// collectParquetKeys lists the parquet object keys named by the
// selector. With explicit keys, every key must end in .parquet. With a
// prefix, the driver listing is filtered to .parquet objects.
func (s *Service) collectParquetKeys(ctx context.Context, bucket, prefix string, keys []string) ([]string, error) {
	if len(keys) > 0 {
		out := make([]string, 0, len(keys))
		for _, k := range keys {
			if !strings.HasSuffix(strings.ToLower(k), ".parquet") {
				return nil, fmt.Errorf("%w: key %q is not a .parquet object", consts.ErrBadRequest, k)
			}
			out = append(out, k)
		}
		return out, nil
	}

	var out []string
	var token string
	for {
		res, err := s.ListObjects(ctx, bucket, ListObjectsOpts{
			Prefix:            prefix,
			ContinuationToken: token,
			MaxKeys:           1000,
		})
		if err != nil {
			return nil, err
		}
		for _, it := range res.Items {
			if strings.HasSuffix(strings.ToLower(it.Key), ".parquet") {
				out = append(out, it.Key)
				if len(out) > maxQuerySources {
					// Surface the cap from resolveQuerySources rather than
					// listing the whole bucket.
					return out, nil
				}
			}
		}
		if !res.IsTruncated || res.NextContinuationToken == "" {
			break
		}
		token = res.NextContinuationToken
	}
	return out, nil
}
