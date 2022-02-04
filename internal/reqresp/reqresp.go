// Package reqresp is used to create mock web servers for testing
package reqresp

import (
	"bytes"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"regexp"
	"testing"

	"github.com/opencontainers/go-digest"
)

type ReqResp struct {
	ReqEntry  ReqEntry
	RespEntry RespEntry
}

type ReqEntry struct {
	Name     string
	DelOnUse bool
	Method   string
	Path     string
	PathRE   *regexp.Regexp
	Query    map[string][]string
	Headers  http.Header
	Body     []byte
}

type RespEntry struct {
	Status  int
	Headers http.Header
	Body    []byte
}

func NewHandler(t *testing.T, rrs []ReqResp) http.Handler {
	r := rrHandler{
		t:   t,
		rrs: rrs,
	}
	return &r
}

type rrHandler struct {
	t   *testing.T
	rrs []ReqResp
}

// return false if any item in a is not found in b
func strMapMatch(a, b map[string][]string) bool {
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		for _, ave := range av {
			found := false
			for _, bve := range bv {
				if ave == bve {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

func (r *rrHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	reqBody, err := ioutil.ReadAll(req.Body)
	if err != nil {
		r.t.Errorf("Error reading request body: %v", err)
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write([]byte("Error reading request body"))
		return
	}
	for i, rr := range r.rrs {
		reqMatch := rr.ReqEntry
		if reqMatch.Method != req.Method ||
			(reqMatch.PathRE != nil && !reqMatch.PathRE.MatchString(req.URL.Path)) ||
			(reqMatch.Path != "" && reqMatch.Path != req.URL.Path) ||
			!strMapMatch(reqMatch.Query, req.URL.Query()) ||
			!strMapMatch(reqMatch.Headers, req.Header) ||
			(len(reqMatch.Body) > 0 && !bytes.Equal(reqMatch.Body, reqBody)) {
			// skip if any field does not match
			continue
		}

		// respond
		r.t.Logf("Sending response %s", reqMatch.Name)
		rwHeader := rw.Header()
		for k, v := range rr.RespEntry.Headers {
			rwHeader[k] = v
		}
		if rr.RespEntry.Status != 0 {
			rw.WriteHeader(rr.RespEntry.Status)
		}
		io.Copy(rw, bytes.NewReader(rr.RespEntry.Body))

		// for single use test cases, delete this entry
		if reqMatch.DelOnUse {
			r.rrs = append(r.rrs[:i], r.rrs[i+1:]...)
		}
		return
	}
	r.t.Errorf("Unhandled request: %v", req)
	rw.WriteHeader(http.StatusInternalServerError)
	rw.Write([]byte("Unsupported request"))
}

// BaseEntries initial entries for a generic docker registry
var BaseEntries = []ReqResp{
	{
		ReqEntry: ReqEntry{
			Method: "GET",
			Path:   "/v2/",
		},
		RespEntry: RespEntry{
			Status: http.StatusOK,
			Headers: http.Header(map[string][]string{
				"Docker-Distribution-API-Version": {"registry/2.0"},
			}),
		},
	},
}

// NewRandomBlob outputs a reproducible random blob (based on the seed) for testing
func NewRandomBlob(size int, seed int64) (digest.Digest, []byte) {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, size)
	if n, err := r.Read(b); err != nil {
		panic(err)
	} else if n != size {
		panic("unable to read enough bytes")
	}
	return digest.FromBytes(b), b
}
