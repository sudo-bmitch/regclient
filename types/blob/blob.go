// Package blob is the underlying type for pushing and pulling blobs
package blob

import (
	"io"
	"net/http"

	ociv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/regclient/regclient/types/ref"
)

// Blob interface is used for returning blobs
type Blob interface {
	Common
	RawBody() ([]byte, error)
}

type blobConfig struct {
	desc   ociv1.Descriptor
	header http.Header
	image  ociv1.Image
	r      ref.Ref
	rdr    io.Reader
	resp   *http.Response
}

type Opts func(*blobConfig)

// WithDesc specifies the descriptor associated with the blob
func WithDesc(d ociv1.Descriptor) Opts {
	return func(bc *blobConfig) {
		bc.desc = d
	}
}

// WithHeader defines the headers received when pulling a blob
func WithHeader(header http.Header) Opts {
	return func(bc *blobConfig) {
		bc.header = header
	}
}

// WithImage provides the OCI Image config needed for config blobs
func WithImage(image ociv1.Image) Opts {
	return func(bc *blobConfig) {
		bc.image = image
	}
}

// WithReader defines the reader for a new blob
func WithReader(rc io.Reader) Opts {
	return func(bc *blobConfig) {
		bc.rdr = rc
	}
}

// WithRef specifies the reference where the blob was pulled from
func WithRef(r ref.Ref) Opts {
	return func(bc *blobConfig) {
		bc.r = r
	}
}

// WithResp includes the http response, which is used to extract the headers and reader
func WithResp(resp *http.Response) Opts {
	return func(bc *blobConfig) {
		bc.resp = resp
		if bc.header == nil {
			bc.header = resp.Header
		}
	}
}
