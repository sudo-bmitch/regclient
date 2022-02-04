// Package scheme defines the interface for various reference schemes
package scheme

import (
	"context"
	"io"

	"github.com/opencontainers/go-digest"
	"github.com/regclient/regclient/types/blob"
	"github.com/regclient/regclient/types/manifest"
	"github.com/regclient/regclient/types/ref"
	"github.com/regclient/regclient/types/tag"
)

// API is used to interface between different methods to store images
type API interface {
	// Info is experimental, do not use
	Info() Info

	// BlobDelete removes a blob from the repository
	BlobDelete(ctx context.Context, r ref.Ref, d digest.Digest) error
	// BlobGet retrieves a blob, returning a reader
	BlobGet(ctx context.Context, r ref.Ref, d digest.Digest) (blob.Reader, error)
	// BlobHead verifies the existence of a blob, the reader contains the headers but no body to read
	BlobHead(ctx context.Context, r ref.Ref, d digest.Digest) (blob.Reader, error)
	// BlobMount attempts to perform a server side copy of the blob
	BlobMount(ctx context.Context, refSrc ref.Ref, refTgt ref.Ref, d digest.Digest) error
	// BlobPut sends a blob to the repository, returns the digest and size when successful
	BlobPut(ctx context.Context, r ref.Ref, d digest.Digest, rdr io.Reader, cl int64) (digest.Digest, int64, error)

	// ManifestDelete removes a manifest, including all tags that point to that manifest
	ManifestDelete(ctx context.Context, r ref.Ref) error
	// ManifestGet retrieves a manifest from a repository
	ManifestGet(ctx context.Context, r ref.Ref) (manifest.Manifest, error)
	// ManifestHead gets metadata about the manifest (existence, digest, mediatype, size)
	ManifestHead(ctx context.Context, r ref.Ref) (manifest.Manifest, error)
	// ManifestPut sends a manifest to the repository
	ManifestPut(ctx context.Context, r ref.Ref, m manifest.Manifest, opts ...ManifestOpts) error

	// TagDelete removes a tag from the repository
	TagDelete(ctx context.Context, r ref.Ref) error
	// TagList returns a list of tags from the repository
	TagList(ctx context.Context, r ref.Ref, opts ...TagOpts) (*tag.List, error)
}

// Closer is used to check if a scheme implements the Close API
type Closer interface {
	Close(ctx context.Context, r ref.Ref) error
}

// Info provides details on the scheme, this is experimental, do not use
type Info struct {
	ManifestPushFirst bool
}

// ManifestConfig is used by schemes to import ManifestOpts
type ManifestConfig struct {
	Child bool // used when pushing a child of a manifest list, skips indexing in ocidir
}

// ManifestOpts is used to set options on manifest APIs
type ManifestOpts func(*ManifestConfig)

// WithManifestChild indicates the API call is on a child manifest
// This is used internally when copying multi-platform manifests
// This bypasses tracking of an untagged digest in ocidir which is needed for garbage collection
func WithManifestChild() ManifestOpts {
	return func(config *ManifestConfig) {
		config.Child = true
	}
}

// RepoConfig is used by schemes to import RepoOpts
type RepoConfig struct {
	Limit int
	Last  string
}

// RepoOpts is used to set options on repo APIs
type RepoOpts func(*RepoConfig)

// WithRepoLimit passes a maximum number of repositories to return to the repository list API
// Registries may ignore this
func WithRepoLimit(l int) RepoOpts {
	return func(config *RepoConfig) {
		config.Limit = l
	}
}

// WithRepoLast passes the last received repository for requesting the next batch of repositories
// Registries may ignore this
func WithRepoLast(l string) RepoOpts {
	return func(config *RepoConfig) {
		config.Last = l
	}
}

// TagConfig is used by schemes to import TagOpts
type TagConfig struct {
	Limit int
	Last  string
}

// TagOpts is used to set options on tag APIs
type TagOpts func(*TagConfig)

// WithTagLimit passes a maximum number of tags to return to the tag list API
// Registries may ignore this
func WithTagLimit(limit int) TagOpts {
	return func(t *TagConfig) {
		t.Limit = limit
	}
}

// WithTagLast passes the last received tag for requesting the next batch of tags
// Registries may ignore this
func WithTagLast(last string) TagOpts {
	return func(t *TagConfig) {
		t.Last = last
	}
}
