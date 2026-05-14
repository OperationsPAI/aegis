// Tests for the List + GetReader additions on Client. We spin up a
// real blob.Service backed by a localfs driver and an in-memory sqlite
// metadata DB, then drive both clients (LocalClient direct, RemoteClient
// through an httptest server) against the same dataset.
package blobclient

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aegis/crud/storage/blob"
	"aegis/platform/consts"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// testFixture wires a localfs-backed blob.Service + LocalClient + an
// httptest server serving the (auth-stripped) handler routes for the
// RemoteClient to call.
type testFixture struct {
	svc    *blob.Service
	local  *LocalClient
	remote *RemoteClient
	bucket string
	server *httptest.Server
}

func newFixture(t *testing.T) *testFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	root := t.TempDir()
	cfg := blob.BucketConfig{
		Name:       "scratch",
		Driver:     "localfs",
		Root:       root,
		PublicRead: true, // bypass InlineGet ACL in test
	}
	drv, err := blob.NewLocalFSDriver(cfg, []byte("test-signing-key"))
	require.NoError(t, err)

	// Sqlite in-memory metadata DB so Service.Get/Stat repo lookups
	// resolve cleanly.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&blob.ObjectRecord{}))
	repo := blob.NewRepository(db)

	reg := buildRegistryWithBucket(cfg, drv)
	svc := blob.NewService(reg, repo, blob.NewClock())
	auth := blob.NewAuthorizer()
	h := blob.NewHandler(svc, auth, blob.RegistryDeps{SigningKey: []byte("test-signing-key")})

	// Mount only the routes our tests hit, with no JWT middleware.
	r := gin.New()
	v2 := r.Group(consts.APIPrefixV2)
	{
		v2.GET("/blob/buckets/:bucket/object-list", h.ListObjects)
		v2.GET("/blob/buckets/:bucket/stream/*key", h.StreamGet)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	remote, err := NewRemoteClient(RemoteClientConfig{BaseURL: srv.URL}, nil)
	require.NoError(t, err)

	return &testFixture{
		svc:    svc,
		local:  NewLocalClient(svc),
		remote: remote,
		bucket: cfg.Name,
		server: srv,
	}
}

// buildRegistryWithBucket constructs a Registry with a single bucket.
// Registry's exported constructor walks viper config, so for tests we
// reach through a small helper exported via test indirection.
func buildRegistryWithBucket(cfg blob.BucketConfig, drv blob.Driver) *blob.Registry {
	return blob.NewTestRegistry(map[string]*blob.Bucket{
		cfg.Name: {Config: cfg, Driver: drv},
	})
}

// seed writes `keys` directly via the driver AND registers the rows
// in the metadata DB so Service.GetReader can resolve them.
func (f *testFixture) seed(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		_, _, err := f.svc.PutBytes(context.Background(), blob.PresignPutInput{
			Bucket: f.bucket, Key: k, ContentType: "text/plain",
		}, strings.NewReader("body-of-"+k))
		require.NoError(t, err, k)
	}
}

// ----- List -----

func TestList_EmptyBucket(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"local", "remote"} {
		t.Run(name, func(t *testing.T) {
			c := pickClient(f, name)
			res, err := c.List(context.Background(), f.bucket, "", ListOpts{})
			require.NoError(t, err)
			require.Empty(t, res.Objects)
			require.False(t, res.IsTruncated)
		})
	}
}

func TestList_PrefixFiltering(t *testing.T) {
	f := newFixture(t)
	f.seed(t, "a/one.txt", "a/two.txt", "b/three.txt")
	for _, name := range []string{"local", "remote"} {
		t.Run(name, func(t *testing.T) {
			c := pickClient(f, name)
			res, err := c.List(context.Background(), f.bucket, "a/", ListOpts{})
			require.NoError(t, err)
			require.Len(t, res.Objects, 2)
			for _, o := range res.Objects {
				require.True(t, strings.HasPrefix(o.Key, "a/"), o.Key)
			}
		})
	}
}

func TestList_Pagination(t *testing.T) {
	f := newFixture(t)
	var keys []string
	for i := 0; i < 5; i++ {
		keys = append(keys, fmt.Sprintf("p/%02d.txt", i))
	}
	f.seed(t, keys...)

	for _, name := range []string{"local", "remote"} {
		t.Run(name, func(t *testing.T) {
			c := pickClient(f, name)
			var got []string
			token := ""
			for page := 0; page < 10; page++ {
				res, err := c.List(context.Background(), f.bucket, "p/",
					ListOpts{MaxKeys: 2, ContinuationToken: token})
				require.NoError(t, err)
				for _, o := range res.Objects {
					got = append(got, o.Key)
				}
				if !res.IsTruncated {
					break
				}
				require.NotEmpty(t, res.NextContinuationToken)
				token = res.NextContinuationToken
			}
			require.ElementsMatch(t, keys, got)
		})
	}
}

func TestList_Delimiter_CommonPrefixes(t *testing.T) {
	f := newFixture(t)
	f.seed(t, "tree/a/1.txt", "tree/a/2.txt", "tree/b/3.txt", "tree/top.txt")
	for _, name := range []string{"local", "remote"} {
		t.Run(name, func(t *testing.T) {
			c := pickClient(f, name)
			res, err := c.List(context.Background(), f.bucket, "tree/",
				ListOpts{Delimiter: "/"})
			require.NoError(t, err)
			// "tree/top.txt" is a direct child; "tree/a/" and "tree/b/"
			// roll up into CommonPrefixes.
			var directKeys []string
			for _, o := range res.Objects {
				directKeys = append(directKeys, o.Key)
			}
			require.ElementsMatch(t, []string{"tree/top.txt"}, directKeys)
			require.ElementsMatch(t, []string{"tree/a/", "tree/b/"}, res.CommonPrefixes)
		})
	}
}

// ----- GetReader -----

func TestGetReader_Bytes(t *testing.T) {
	f := newFixture(t)
	f.seed(t, "doc/hello.txt")
	for _, name := range []string{"local", "remote"} {
		t.Run(name, func(t *testing.T) {
			c := pickClient(f, name)
			rc, meta, err := c.GetReader(context.Background(), f.bucket, "doc/hello.txt")
			require.NoError(t, err)
			t.Cleanup(func() { _ = rc.Close() })
			body, err := io.ReadAll(rc)
			require.NoError(t, err)
			require.Equal(t, "body-of-doc/hello.txt", string(body))
			require.NotNil(t, meta)
			require.Equal(t, int64(len("body-of-doc/hello.txt")), meta.Size)
		})
	}
}

func TestGetReader_Missing(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"local", "remote"} {
		t.Run(name, func(t *testing.T) {
			c := pickClient(f, name)
			_, _, err := c.GetReader(context.Background(), f.bucket, "nope.txt")
			require.Error(t, err)
		})
	}
}

func pickClient(f *testFixture, name string) Client {
	if name == "local" {
		return f.local
	}
	return f.remote
}

// silence unused-import lint if filepath ever becomes unused.
var _ = filepath.Join
