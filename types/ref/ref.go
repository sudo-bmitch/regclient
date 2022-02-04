// Package ref is used to define references
// References default to remote registry references (registry:port/repo:tag)
// Schemes can be included in front of the reference for different reference types
package ref

import (
	"fmt"
	"regexp"

	"github.com/docker/distribution/reference"
)

var (
	pathS    = `[/a-zA-Z0-9_\-. ]+`
	tagS     = `[\w][\w.-]{0,127}`
	digestS  = `[A-Za-z][A-Za-z0-9]*(?:[-_+.][A-Za-z][A-Za-z0-9]*)*[:][[:xdigit:]]{32,}`
	schemeRE = regexp.MustCompile(`^([a-z]+)://(.+)$`)
	pathRE   = regexp.MustCompile(`^(` + pathS + `)` +
		`(?:` + regexp.QuoteMeta(`:`) + `(` + tagS + `))?` +
		`(?:` + regexp.QuoteMeta(`@`) + `(` + digestS + `))?$`)
)

// Ref reference to a registry/repository
// If the tag or digest is available, it's also included in the reference.
// Reference itself is the unparsed string.
// While this is currently a struct, that may change in the future and access
// to contents should not be assumed/used.
type Ref struct {
	Scheme     string
	Reference  string // unparsed string
	Registry   string // server, host:port
	Repository string // path on server
	Tag        string
	Digest     string
	Path       string
}

// New returns a reference based on the scheme, defaulting to a
func New(ref string) (Ref, error) {
	scheme := ""
	path := ref
	matchScheme := schemeRE.FindStringSubmatch(ref)
	if len(matchScheme) == 3 {
		scheme = matchScheme[1]
		path = matchScheme[2]
	}
	ret := Ref{
		Scheme:    scheme,
		Reference: ref,
	}
	switch scheme {
	case "":
		ret.Scheme = "reg"
		parsed, err := reference.ParseNormalizedNamed(ref)
		if err != nil {
			return ret, err
		}
		ret.Registry = reference.Domain(parsed)
		ret.Repository = reference.Path(parsed)
		if canonical, ok := parsed.(reference.Canonical); ok {
			ret.Digest = canonical.Digest().String()
		}
		if tagged, ok := parsed.(reference.Tagged); ok {
			ret.Tag = tagged.Tag()
		}
		if ret.Tag == "" && ret.Digest == "" {
			ret.Tag = "latest"
		}

	case "ocidir", "ocifile":
		matchPath := pathRE.FindStringSubmatch(path)
		if matchPath == nil || len(matchPath) < 2 || matchPath[1] == "" {
			return Ref{}, fmt.Errorf("invalid path for scheme \"%s\": %s", scheme, path)
		}
		ret.Path = matchPath[1]
		if len(matchPath) > 2 && matchPath[2] != "" {
			ret.Tag = matchPath[2]
		}
		if len(matchPath) > 3 && matchPath[3] != "" {
			ret.Digest = matchPath[3]
		}

	default:
		return Ref{}, fmt.Errorf("unhandled reference scheme \"%s\" in \"%s\"", scheme, ref)
	}
	return ret, nil
}

// CommonName outputs a parsable name from a reference
func (r Ref) CommonName() string {
	cn := ""
	switch r.Scheme {
	case "reg":
		if r.Registry != "" {
			cn = r.Registry + "/"
		}
		if r.Repository == "" {
			return ""
		}
		cn = cn + r.Repository
		if r.Digest != "" {
			cn = cn + "@" + r.Digest
		} else if r.Tag != "" {
			cn = cn + ":" + r.Tag
		}
	case "ocidir":
		cn = fmt.Sprintf("ocidir://%s", r.Path)
		if r.Digest != "" {
			cn = cn + "@" + r.Digest
		} else if r.Tag != "" {
			cn = cn + ":" + r.Tag
		}
	}
	return cn
}

// EqualRegistry compares the registry between two references
func EqualRegistry(a, b Ref) bool {
	if a.Scheme != b.Scheme {
		return false
	}
	switch a.Scheme {
	case "reg":
		return a.Registry == b.Registry
	case "ocidir":
		return a.Path == b.Path
	default:
		return false
	}
}

// EqualRepository compares the repository between two references
func EqualRepository(a, b Ref) bool {
	if a.Scheme != b.Scheme {
		return false
	}
	switch a.Scheme {
	case "reg":
		return a.Registry == b.Registry && a.Repository == b.Repository
	case "ocidir":
		return a.Path == b.Path
	default:
		return false
	}
}
