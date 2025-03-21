package main

import (
	"archive/tar"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"

	"github.com/regclient/regclient"
	"github.com/regclient/regclient/internal/ascii"
	"github.com/regclient/regclient/internal/strparse"
	"github.com/regclient/regclient/internal/units"
	"github.com/regclient/regclient/mod"
	"github.com/regclient/regclient/pkg/archive"
	"github.com/regclient/regclient/pkg/template"
	"github.com/regclient/regclient/types"
	"github.com/regclient/regclient/types/blob"
	"github.com/regclient/regclient/types/descriptor"
	"github.com/regclient/regclient/types/docker/schema2"
	"github.com/regclient/regclient/types/errs"
	"github.com/regclient/regclient/types/manifest"
	"github.com/regclient/regclient/types/mediatype"
	v1 "github.com/regclient/regclient/types/oci/v1"
	"github.com/regclient/regclient/types/platform"
	"github.com/regclient/regclient/types/ref"
	"github.com/regclient/regclient/types/warning"
)

type imageOpts struct {
	rootOpts        *rootOpts
	annotations     []string
	byDigest        bool
	checkBaseRef    string
	checkBaseDigest string
	checkSkipConfig bool
	create          string
	created         string
	digestTags      bool
	exportCompress  bool
	exportRef       string
	fastCheck       bool
	forceRecursive  bool
	format          string
	importName      string
	includeExternal bool
	labels          []string
	mediaType       string
	modOpts         []mod.Opts
	platform        string
	platforms       []string
	quiet           bool
	referrers       bool
	referrerSrc     string
	referrerTgt     string
	replace         bool
}

var imageKnownTypes = []string{
	mediatype.OCI1Manifest,
	mediatype.Docker2Manifest,
}

func NewImageCmd(rOpts *rootOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image <cmd>",
		Short: "manage images",
	}
	cmd.AddCommand(newImageCheckBaseCmd(rOpts))
	cmd.AddCommand(newImageCopyCmd(rOpts))
	cmd.AddCommand(newImageCreateCmd(rOpts))
	cmd.AddCommand(newImageDeleteCmd(rOpts))
	cmd.AddCommand(newImageDigestCmd(rOpts))
	cmd.AddCommand(newImageExportCmd(rOpts))
	cmd.AddCommand(newImageGetFileCmd(rOpts))
	cmd.AddCommand(newImageImportCmd(rOpts))
	cmd.AddCommand(newImageInspectCmd(rOpts))
	cmd.AddCommand(newImageManifestCmd(rOpts))
	cmd.AddCommand(newImageModCmd(rOpts))
	cmd.AddCommand(newImageRateLimitCmd(rOpts))
	return cmd
}

func newImageCheckBaseCmd(rOpts *rootOpts) *cobra.Command {
	opts := imageOpts{
		rootOpts: rOpts,
	}
	cmd := &cobra.Command{
		Use:     "check-base <image_ref>",
		Aliases: []string{},
		Short:   "check if the base image has changed",
		Long: `Check the base image (found using annotations or an option).
If the base name is not provided, annotations will be checked in the image.
If the digest is available, this checks if that matches the base name.
If the digest is not available, layers of each manifest are compared.
If the layers match, the config (history and roots) are optionally compared.	
If the base image does not match, the command exits with a non-zero status.`,
		Example: `
# report if base image has changed using annotations
regctl image check-base ghcr.io/regclient/regctl:alpine

# suppress the normal output with --quiet for scripts
if ! regctl image check-base ghcr.io/regclient/regctl:alpine --quiet; then
  echo build a new image here
fi`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: rOpts.completeArgTag,
		RunE:              opts.runImageCheckBase,
	}
	cmd.Flags().StringVar(&opts.checkBaseRef, "base", "", "Base image reference (including tag)")
	cmd.Flags().StringVar(&opts.checkBaseDigest, "digest", "", "Base image digest (checks if digest matches base)")
	cmd.Flags().BoolVar(&opts.checkSkipConfig, "no-config", false, "Skip check of config history")
	cmd.Flags().StringVarP(&opts.platform, "platform", "p", "", "Specify platform (e.g. linux/amd64 or local)")
	cmd.Flags().BoolVar(&opts.quiet, "quiet", false, "Do not output to stdout")
	return cmd
}

func newImageCopyCmd(rOpts *rootOpts) *cobra.Command {
	opts := imageOpts{
		rootOpts: rOpts,
	}
	cmd := &cobra.Command{
		Use:     "copy <src_image_ref> <dst_image_ref>",
		Aliases: []string{"cp"},
		Short:   "copy or retag image",
		Long: `Copy or retag an image. This works between registries and only pulls layers
that do not exist at the target. In the same registry it attempts to mount
the layers between repositories. And within the same repository it only
sends the manifest with the new tag.`,
		Example: `
# copy an image
regctl image copy \
  ghcr.io/regclient/regctl:edge registry.example.org/regclient/regctl:edge

# copy an image with signatures
regctl image copy --digest-tags \
  ghcr.io/regclient/regctl:edge registry.example.org/regclient/regctl:edge

# copy only the local platform image
regctl image copy --platform local \
  ghcr.io/regclient/regctl:edge registry.example.org/regclient/regctl:edge

# retag an image
regctl image copy registry.example.org/repo:v1.2.3 registry.example.org/repo:v1

# copy an image to an OCI Layout including referrers
regctl image copy --referrers \
  ghcr.io/regclient/regctl:edge ocidir://regctl:edge

# copy a windows image, including foreign layers
regctl image copy --platform windows/amd64,osver=10.0.17763.4974 --include-external \
  golang:latest registry.example.org/library/golang:windows`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: rOpts.completeArgTag,
		RunE:              opts.runImageCopy,
	}
	cmd.Flags().BoolVar(&opts.digestTags, "digest-tags", false, "Include digest tags (\"sha256-<digest>.*\") when copying manifests")
	cmd.Flags().BoolVar(&opts.fastCheck, "fast", false, "Fast check, skip referrers and digest tag checks when image exists, overrides force-recursive")
	cmd.Flags().BoolVar(&opts.forceRecursive, "force-recursive", false, "Force recursive copy of image, repairs missing nested blobs and manifests")
	cmd.Flags().StringVar(&opts.format, "format", "", "Format output with go template syntax")
	_ = cmd.RegisterFlagCompletionFunc("format", completeArgNone)
	cmd.Flags().BoolVar(&opts.includeExternal, "include-external", false, "Include external layers")
	cmd.Flags().StringVarP(&opts.platform, "platform", "p", "", "Specify platform (e.g. linux/amd64 or local)")
	_ = cmd.RegisterFlagCompletionFunc("platform", completeArgPlatform)
	cmd.Flags().StringArrayVar(&opts.platforms, "platforms", []string{}, "Copy only specific platforms, registry validation must be disabled")
	// platforms should be treated as experimental since it will break many registries
	_ = cmd.Flags().MarkHidden("platforms")
	cmd.Flags().BoolVar(&opts.referrers, "referrers", false, "Include referrers")
	cmd.Flags().StringVar(&opts.referrerSrc, "referrers-src", "", "External source for referrers")
	cmd.Flags().StringVar(&opts.referrerTgt, "referrers-tgt", "", "External target for referrers")
	return cmd
}

func newImageCreateCmd(rOpts *rootOpts) *cobra.Command {
	opts := imageOpts{
		rootOpts: rOpts,
	}
	cmd := &cobra.Command{
		Use:     "create <image_ref>",
		Aliases: []string{"init", "new"},
		Short:   "create a new image manifest",
		Long:    `Create a new image manifest from an initially empty (scratch) state.`,
		Example: `
# create a scratch image
regctl image create ocidir://new-image:scratch
`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: rOpts.completeArgTag,
		RunE:              opts.runImageCreate,
	}
	cmd.Flags().StringArrayVar(&opts.annotations, "annotation", []string{}, "Annotation to set on manifest")
	cmd.Flags().BoolVar(&opts.byDigest, "by-digest", false, "Push manifest by digest instead of tag")
	cmd.Flags().StringVar(&opts.created, "created", "", "Created timestamp to set (use \"now\" or RFC3339 syntax)")
	cmd.Flags().StringVar(&opts.format, "format", "", "Format output with go template syntax")
	_ = cmd.RegisterFlagCompletionFunc("format", completeArgNone)
	cmd.Flags().StringArrayVar(&opts.labels, "label", []string{}, "Labels to set in the image config")
	cmd.Flags().StringVar(&opts.mediaType, "media-type", mediatype.OCI1Manifest, "Media-type for manifest")
	_ = cmd.RegisterFlagCompletionFunc("media-type", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return imageKnownTypes, cobra.ShellCompDirectiveNoFileComp
	})
	cmd.Flags().StringVar(&opts.platform, "platform", "", "Platform to set on the image")
	_ = cmd.RegisterFlagCompletionFunc("platform", completeArgPlatform)
	return cmd
}

func newImageDeleteCmd(rOpts *rootOpts) *cobra.Command {
	cmd := newManifestDeleteCmd(rOpts)
	cmd.Short = "delete image, same as \"manifest delete\""
	return cmd
}

func newImageDigestCmd(rOpts *rootOpts) *cobra.Command {
	cmd := newManifestHeadCmd(rOpts)
	cmd.Use = "digest <image_ref>"
	cmd.Short = "show digest for pinning, same as \"regctl manifest digest\""
	cmd.Aliases = []string{}
	cmd.Flag("require-digest").DefValue = "true"
	return cmd
}

func newImageExportCmd(rOpts *rootOpts) *cobra.Command {
	opts := imageOpts{
		rootOpts: rOpts,
	}
	cmd := &cobra.Command{
		Use:   "export <image_ref> [filename]",
		Short: "export image",
		Long: `Exports an image into a tar file that can be later loaded into a docker
engine with "docker load". The tar file is output to stdout by default.
Compression is typically not useful since layers are already compressed.`,
		Example: `
# export an image
regctl image export registry.example.org/repo:v1 >image-v1.tar`,
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: rOpts.completeArgTag,
		RunE:              opts.runImageExport,
	}
	cmd.Flags().BoolVar(&opts.exportCompress, "compress", false, "Compress output with gzip")
	cmd.Flags().StringVar(&opts.exportRef, "name", "", "Name of image to embed for docker load")
	cmd.Flags().StringVarP(&opts.platform, "platform", "p", "", "Specify platform (e.g. linux/amd64 or local)")
	_ = cmd.RegisterFlagCompletionFunc("platform", completeArgPlatform)
	return cmd
}

func newImageGetFileCmd(rOpts *rootOpts) *cobra.Command {
	opts := imageOpts{
		rootOpts: rOpts,
	}
	cmd := &cobra.Command{
		Use:     "get-file <image_ref> <filename> [out-file]",
		Aliases: []string{"cat"},
		Short:   "get a file from an image",
		Long:    `Go through each of the image layers searching for the requested file.`,
		Example: `
# get the alpine-release file from the latest alpine image
regctl image get-file --platform local alpine /etc/alpine-release`,
		Args:              cobra.RangeArgs(2, 3),
		ValidArgsFunction: completeArgList([]completeFunc{rOpts.completeArgTag, completeArgNone, completeArgNone}),
		RunE:              opts.runImageGetFile,
	}
	cmd.Flags().StringVar(&opts.format, "format", "", "Format output with go template syntax")
	_ = cmd.RegisterFlagCompletionFunc("format", completeArgNone)
	cmd.Flags().StringVarP(&opts.platform, "platform", "p", "", "Specify platform (e.g. linux/amd64 or local)")
	_ = cmd.RegisterFlagCompletionFunc("platform", completeArgPlatform)
	return cmd
}

func newImageImportCmd(rOpts *rootOpts) *cobra.Command {
	opts := imageOpts{
		rootOpts: rOpts,
	}
	cmd := &cobra.Command{
		Use:   "import <image_ref> <filename>",
		Short: "import image",
		Long: `Imports an image from a tar file. This must be either a docker formatted tar
from "docker save" or an OCI Layout compatible tar. The output from
"regctl image export" can be used. Stdin is not permitted for the tar file.`,
		Example: `
# import an image saved from docker
regctl image import registry.example.org/repo:v1 image-v1.tar`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeArgList([]completeFunc{rOpts.completeArgTag, completeArgDefault}),
		RunE:              opts.runImageImport,
	}
	cmd.Flags().StringVar(&opts.importName, "name", "", "Name of image or tag to import when multiple images are packaged in the tar")
	return cmd
}

func newImageInspectCmd(rOpts *rootOpts) *cobra.Command {
	opts := imageOpts{
		rootOpts: rOpts,
	}
	cmd := &cobra.Command{
		Use:     "inspect <image_ref>",
		Aliases: []string{"config"},
		Short:   "inspect image",
		Long: `Shows the config json for an image and is equivalent to pulling the image
in docker, and inspecting it, but without pulling any of the image layers.`,
		Example: `
# return the image config for the nginx image
regctl image inspect --platform local nginx`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: rOpts.completeArgTag,
		RunE:              opts.runImageInspect,
	}
	cmd.Flags().StringVar(&opts.format, "format", "{{printPretty .}}", "Format output with go template syntax")
	_ = cmd.RegisterFlagCompletionFunc("format", completeArgNone)
	cmd.Flags().StringVarP(&opts.platform, "platform", "p", "", "Specify platform (e.g. linux/amd64 or local)")
	_ = cmd.RegisterFlagCompletionFunc("platform", completeArgPlatform)
	return cmd
}

func newImageManifestCmd(rOpts *rootOpts) *cobra.Command {
	cmd := newManifestGetCmd(rOpts)
	cmd.Use = "manifest <image_ref>"
	cmd.Short = "show manifest or manifest list, same as \"manifest get\""
	cmd.Aliases = []string{}
	return cmd
}

func newImageModCmd(rOpts *rootOpts) *cobra.Command {
	opts := imageOpts{
		rootOpts: rOpts,
	}
	cmd := &cobra.Command{
		Use:   "mod <image_ref>",
		Short: "modify an image",
		// TODO: remove EXPERIMENTAL when stable
		Long: `EXPERIMENTAL: Applies requested modifications to an image

  For time options, the value is a comma separated list of key/value pairs:
  set=${time}: time to set in rfc3339 format, e.g. 2006-01-02T15:04:05Z
  from-label=${label}: label used to extract time in rfc3339 format
  after=${time_in_rfc3339}: adjust any time after this
  base-ref=${image}: image to lookup base layers, which are skipped
  base-layers=${count}: number of layers to skip changing (from the base image)
  Note: set or from-label is required in the time options`,
		Example: `
# add an annotation to all images, replacing the v1 tag with the new image
regctl image mod registry.example.org/repo:v1 \
  --replace --annotation "[*]org.opencontainers.image.created=2021-02-03T05:06:07Z"

# convert an image to the OCI media types, copying to local registry
regctl image mod alpine:3.5 --to-oci --create registry.example.org/alpine:3.5

# append a layer to only the linux/amd64 image using the file.tar contents
regctl image mod registry.example.org/repo:v1 --create v1-extended \
  --layer-add "tar=file.tar,platform=linux/amd64"

# append a layer to all platforms using the contents of a directory
regctl image mod registry.example.org/repo:v1 --create v1-extended \
  --layer-add "dir=path/to/directory"

# set the timestamp on the config and layers, ignoring the alpine base image layers
regctl image mod registry.example.org/repo:v1 --create v1-time \
  --time "set=2021-02-03T04:05:06Z,base-ref=alpine:3"

# set the entrypoint to be bash and unset the default command
regctl image mod registry.example.org/repo:v1 --create v1-bash \
  --config-entrypoint '["bash"]' --config-cmd ""

# delete an environment variable from only the linux/arm64 image
regctl image mod registry.example.org/repo:v1 --create v1-env \
  --env "[linux/arm64]LD_PRELOAD="

# Rebase an older regctl image, copying to the local registry.
# This uses annotations that were included in the original image build.
regctl image mod registry.example.org/regctl:v0.5.1-alpine \
  --rebase --create v0.5.1-alpine-rebase`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: rOpts.completeArgTag,
		RunE:              opts.runImageMod,
	}
	opts.modOpts = []mod.Opts{}
	cmd.Flags().StringVar(&opts.create, "create", "", "Create image or tag")
	cmd.Flags().BoolVar(&opts.replace, "replace", false, "Replace tag (ignored when \"create\" is used)")
	// most image mod flags are order dependent, so they are added using VarP/VarPF to append to modOpts
	cmd.Flags().Var(&modFlagFunc{
		t: "stringArray",
		f: func(val string) error {
			vs := strings.SplitN(val, "=", 2)
			if len(vs) == 2 {
				opts.modOpts = append(opts.modOpts, mod.WithAnnotation(vs[0], vs[1]))
			} else if len(vs) == 1 {
				opts.modOpts = append(opts.modOpts, mod.WithAnnotation(vs[0], ""))
			} else {
				return fmt.Errorf("invalid annotation")
			}
			return nil
		},
	}, "annotation", `set an annotation (name=value, omit value to delete, prefix with platform list [p1,p2] or [*] for all images)`)
	cmd.Flags().Var(&modFlagFunc{
		t: "stringArray",
		f: func(val string) error {
			vs := strings.SplitN(val, ",", 2)
			if len(vs) < 1 {
				return fmt.Errorf("arg requires an image name and digest")
			}
			r, err := ref.New(vs[0])
			if err != nil {
				return fmt.Errorf("invalid image reference: %w", err)
			}
			d := digest.Digest("")
			if len(vs) == 1 {
				// parse ref with digest
				if r.Tag == "" || r.Digest == "" {
					return fmt.Errorf("arg requires an image name and digest")
				}
				d, err = digest.Parse(r.Digest)
				if err != nil {
					return fmt.Errorf("invalid digest: %w", err)
				}
				r.Digest = ""
			} else {
				// parse separate ref and digest
				d, err = digest.Parse(vs[1])
				if err != nil {
					return fmt.Errorf("invalid digest: %w", err)
				}
			}
			opts.modOpts = append(opts.modOpts, mod.WithAnnotationOCIBase(r, d))
			return nil
		},
	}, "annotation-base", `set base image annotations (image/name:tag,sha256:digest)`)
	flagAnnotationPromote := cmd.Flags().VarPF(&modFlagFunc{
		t: "bool",
		f: func(val string) error {
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("unable to parse value %s: %w", val, err)
			}
			if b {
				opts.modOpts = append(opts.modOpts, mod.WithAnnotationPromoteCommon())
			}
			return nil
		},
	}, "annotation-promote", "", `promote common annotations from child images to index`)
	flagAnnotationPromote.NoOptDefVal = "true"
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			vs := strings.SplitN(val, "=", 2)
			if len(vs) != 2 {
				return fmt.Errorf("arg must be in the format \"name=value\"")
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithBuildArgRm(vs[0], regexp.MustCompile(regexp.QuoteMeta(vs[1]))))
			return nil
		},
	}, "buildarg-rm", `delete a build arg`)
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			vs := strings.SplitN(val, "=", 2)
			if len(vs) != 2 {
				return fmt.Errorf("arg must be in the format \"name=regex\"")
			}
			value, err := regexp.Compile(vs[1])
			if err != nil {
				return fmt.Errorf("regexp value is invalid: %w", err)
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithBuildArgRm(vs[0], value))
			return nil
		},
	}, "buildarg-rm-regex", `delete a build arg with a regex value`)
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			vSlice := []string{}
			err := json.Unmarshal([]byte(val), &vSlice)
			if err != nil && val != "" {
				vSlice = []string{"/bin/sh", "-c", val}
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithConfigCmd(vSlice),
			)
			return nil
		},
	}, "config-cmd", `set command in the config (json array or string, empty string to delete)`)
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			vSlice := []string{}
			err := json.Unmarshal([]byte(val), &vSlice)
			if err != nil && val != "" {
				vSlice = []string{"/bin/sh", "-c", val}
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithConfigEntrypoint(vSlice),
			)
			return nil
		},
	}, "config-entrypoint", `set entrypoint in the config (json array or string, empty string to delete)`)
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			p, err := platform.Parse(val)
			if err != nil {
				return err
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithConfigPlatform(p),
			)
			return nil
		},
	}, "config-platform", `set platform on the config (not recommended for an index of multiple images)`)
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			ot, otherFields, err := imageParseOptTime(val)
			if err != nil {
				return err
			}
			if len(otherFields) > 0 {
				keys := []string{}
				for k := range otherFields {
					keys = append(keys, k)
				}
				return fmt.Errorf("unknown time option: %s", strings.Join(keys, ", "))
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithConfigTimestamp(ot),
			)
			return nil
		},
	}, "config-time", `set timestamp for the config`)
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			t, err := time.Parse(time.RFC3339, val)
			if err != nil {
				return fmt.Errorf("time must be formatted %s: %w", time.RFC3339, err)
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithConfigTimestamp(mod.OptTime{
					Set:   t,
					After: t,
				}))
			return nil
		},
	}, "config-time-max", `max timestamp for a config`)
	_ = cmd.Flags().MarkHidden("config-time-max") // TODO: deprecate config-time-max in favor of config-time
	cmd.Flags().Var(&modFlagFunc{
		t: "stringArray",
		f: func(val string) error {
			size, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return fmt.Errorf("unable to parse layer size %s: %w", val, err)
			}
			opts.modOpts = append(opts.modOpts, mod.WithData(size))
			return nil
		},
	}, "data-max", `sets or removes descriptor data field (size in bytes)`)
	cmd.Flags().Var(&modFlagFunc{
		t: "stringArray",
		f: func(val string) error {
			opts.modOpts = append(opts.modOpts, mod.WithDigestAlgo(digest.Algorithm(val)))
			return nil
		},
	}, "digest-algo", `change the digest algorithm (sha256, sha512)`)
	cmd.Flags().Var(&modFlagFunc{
		t: "stringArray",
		f: func(val string) error {
			vs := strings.SplitN(val, "=", 2)
			if len(vs) == 2 {
				opts.modOpts = append(opts.modOpts, mod.WithEnv(vs[0], vs[1]))
			} else if len(vs) == 1 {
				opts.modOpts = append(opts.modOpts, mod.WithEnv(vs[0], ""))
			} else {
				return fmt.Errorf("invalid env")
			}
			return nil
		},
	}, "env", `set an environment variable (name=value, omit value to delete, prefix with platform list [p1,p2] for subset of images)`)
	cmd.Flags().Var(&modFlagFunc{
		t: "stringArray",
		f: func(val string) error {
			opts.modOpts = append(opts.modOpts, mod.WithExposeAdd(val))
			return nil
		},
	}, "expose-add", `add an exposed port`)
	cmd.Flags().Var(&modFlagFunc{
		t: "stringArray",
		f: func(val string) error {
			opts.modOpts = append(opts.modOpts, mod.WithExposeRm(val))
			return nil
		},
	}, "expose-rm", `delete an exposed port`)
	flagExtURLsRm := cmd.Flags().VarPF(&modFlagFunc{
		t: "bool",
		f: func(val string) error {
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("unable to parse value %s: %w", val, err)
			}
			if b {
				opts.modOpts = append(opts.modOpts, mod.WithExternalURLsRm())
			}
			return nil
		},
	}, "external-urls-rm", "", `remove external url references from layers (first copy image with "--include-external")`)
	flagExtURLsRm.NoOptDefVal = "true"
	cmd.Flags().Var(&modFlagFunc{
		t: "stringArray",
		f: func(val string) error {
			ot, otherFields, err := imageParseOptTime(val)
			if err != nil {
				return err
			}
			if otherFields["filename"] == "" {
				return fmt.Errorf("filename must be included")
			}
			if len(otherFields) > 1 {
				keys := []string{}
				for k := range otherFields {
					if k != "filename" {
						keys = append(keys, k)
					}
				}
				return fmt.Errorf("unknown time option: %s", strings.Join(keys, ", "))
			}
			opts.modOpts = append(opts.modOpts, mod.WithFileTarTime(otherFields["filename"], ot))
			return nil
		},
	}, "file-tar-time", `timestamp for contents of a tar file within a layer, set filename=${name} with time options`)
	cmd.Flags().Var(&modFlagFunc{
		t: "stringArray",
		f: func(val string) error {
			vs := strings.SplitN(val, ",", 2)
			if len(vs) != 2 {
				return fmt.Errorf("filename and timestamp both required, comma separated")
			}
			t, err := time.Parse(time.RFC3339, vs[1])
			if err != nil {
				return fmt.Errorf("time must be formatted %s: %w", time.RFC3339, err)
			}
			opts.modOpts = append(opts.modOpts, mod.WithFileTarTime(vs[0], mod.OptTime{
				Set:   t,
				After: t,
			}))
			return nil
		},
	}, "file-tar-time-max", `max timestamp for contents of a tar file within a layer`)
	_ = cmd.Flags().MarkHidden("file-tar-time-max") // TODO: deprecate in favor of file-tar-time
	cmd.Flags().Var(&modFlagFunc{
		t: "stringArray",
		f: func(val string) error {
			vs := strings.SplitN(val, "=", 2)
			if len(vs) == 2 {
				opts.modOpts = append(opts.modOpts, mod.WithLabel(vs[0], vs[1]))
			} else if len(vs) == 1 {
				opts.modOpts = append(opts.modOpts, mod.WithLabel(vs[0], ""))
			} else {
				return fmt.Errorf("invalid label")
			}
			return nil
		},
	}, "label", `set an label (name=value, omit value to delete, prefix with platform list [p1,p2] for subset of images)`)
	flagLabelAnnot := cmd.Flags().VarPF(&modFlagFunc{
		t: "bool",
		f: func(val string) error {
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("unable to parse value %s: %w", val, err)
			}
			if b {
				opts.modOpts = append(opts.modOpts, mod.WithLabelToAnnotation())
			}
			return nil
		},
	}, "label-to-annotation", "", `set annotations from labels`)
	flagLabelAnnot.NoOptDefVal = "true"
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			kvSplit, err := strparse.SplitCSKV(val)
			if err != nil {
				return fmt.Errorf("failed to parse layer-add options %s", val)
			}
			var rdr io.Reader
			var mt string
			var platforms []platform.Platform
			if filename, ok := kvSplit["tar"]; ok {
				//#nosec G304 command is run by a user accessing their own files
				fh, err := os.Open(filename)
				if err != nil {
					return fmt.Errorf("failed to open tar file %s: %v", filename, err)
				}
				rdr = fh
				cobra.OnFinalize(func() {
					_ = fh.Close()
				})
			}
			if dir, ok := kvSplit["dir"]; ok {
				if rdr != nil {
					return fmt.Errorf("cannot use dir and tar options together in layer-add")
				}
				pr, pw := io.Pipe()
				go func() {
					err := archive.Tar(context.TODO(), dir, pw)
					if err != nil {
						_ = pw.CloseWithError(err)
					}
					_ = pw.Close()
				}()
				rdr = pr
				cobra.OnFinalize(func() {
					_ = pr.Close()
				})
			}
			if rdr == nil {
				return fmt.Errorf("tar file input is required")
			}
			if mtArg, ok := kvSplit["mediaType"]; ok {
				mt = mtArg
			}
			if pStr, ok := kvSplit["platform"]; ok {
				p, err := platform.Parse(pStr)
				if err != nil {
					return fmt.Errorf("failed to parse platform %s: %v", pStr, err)
				}
				platforms = append(platforms, p)
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithLayerAddTar(rdr, mt, platforms))
			return nil
		},
	}, "layer-add", `add a new layer (tar=file,dir=directory,platform=val)`)
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			var algo archive.CompressType
			err := algo.UnmarshalText([]byte(val))
			if err != nil {
				return fmt.Errorf("unknown layer compression %s", val)
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithLayerCompression(algo))
			return nil
		},
	}, "layer-compress", `change layer compression (gzip, none, zstd)`)
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			re, err := regexp.Compile(val)
			if err != nil {
				return fmt.Errorf("value must be a valid regex: %w", err)
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithLayerRmCreatedBy(*re))
			return nil
		},
	}, "layer-rm-created-by", `delete a layer based on history (created by string is a regex)`)
	cmd.Flags().Var(&modFlagFunc{
		t: "uint",
		f: func(val string) error {
			i, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("index invalid: %w", err)
			}
			opts.modOpts = append(opts.modOpts, mod.WithLayerRmIndex(i))
			return nil
		},
	}, "layer-rm-index", `delete a layer from an image (index begins at 0)`)
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			opts.modOpts = append(opts.modOpts, mod.WithLayerStripFile(val))
			return nil
		},
	}, "layer-strip-file", `delete a file or directory from all layers`)
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			ot, otherFields, err := imageParseOptTime(val)
			if err != nil {
				return err
			}
			if len(otherFields) > 0 {
				keys := []string{}
				for k := range otherFields {
					keys = append(keys, k)
				}
				return fmt.Errorf("unknown time option: %s", strings.Join(keys, ", "))
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithLayerTimestamp(ot),
			)
			return nil
		},
	}, "layer-time", `set timestamp for the layer contents`)
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			t, err := time.Parse(time.RFC3339, val)
			if err != nil {
				return fmt.Errorf("time must be formatted %s: %w", time.RFC3339, err)
			}
			opts.modOpts = append(opts.modOpts, mod.WithLayerTimestamp(
				mod.OptTime{
					Set:   t,
					After: t,
				}))
			return nil
		},
	}, "layer-time-max", `max timestamp for a layer`)
	_ = cmd.Flags().MarkHidden("layer-time-max") // TODO: deprecate in favor of layer-time
	flagRebase := cmd.Flags().VarPF(&modFlagFunc{
		t: "bool",
		f: func(val string) error {
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("unable to parse value %s: %w", val, err)
			}
			if !b {
				return nil
			}
			// pull the manifest, get the base image annotations
			opts.modOpts = append(opts.modOpts, mod.WithRebase())
			return nil
		},
	}, "rebase", "", `rebase an image using OCI annotations`)
	flagRebase.NoOptDefVal = "true"
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			vs := strings.SplitN(val, ",", 2)
			if len(vs) != 2 {
				return fmt.Errorf("rebase-ref requires two base images (old,new), comma separated")
			}
			// parse both refs
			rOld, err := ref.New(vs[0])
			if err != nil {
				return fmt.Errorf("failed parsing old base image ref: %w", err)
			}
			rNew, err := ref.New(vs[1])
			if err != nil {
				return fmt.Errorf("failed parsing new base image ref: %w", err)
			}
			opts.modOpts = append(opts.modOpts, mod.WithRebaseRefs(rOld, rNew))
			return nil
		},
	}, "rebase-ref", `rebase an image with base references (base:old,base:new)`)
	flagReproducible := cmd.Flags().VarPF(&modFlagFunc{
		t: "bool",
		f: func(val string) error {
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("unable to parse value %s: %w", val, err)
			}
			if b {
				opts.modOpts = append(opts.modOpts, mod.WithLayerReproducible())
			}
			return nil
		},
	}, "reproducible", "", `fix tar headers for reproducibility`)
	flagReproducible.NoOptDefVal = "true"
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			ot, otherFields, err := imageParseOptTime(val)
			if err != nil {
				return err
			}
			if len(otherFields) > 0 {
				keys := []string{}
				for k := range otherFields {
					keys = append(keys, k)
				}
				return fmt.Errorf("unknown time option: %s", strings.Join(keys, ", "))
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithConfigTimestamp(ot),
				mod.WithLayerTimestamp(ot),
			)
			return nil
		},
	}, "time", `set timestamp for both the config and layers`)
	cmd.Flags().Var(&modFlagFunc{
		t: "string",
		f: func(val string) error {
			t, err := time.Parse(time.RFC3339, val)
			if err != nil {
				return fmt.Errorf("time must be formatted %s: %w", time.RFC3339, err)
			}
			opts.modOpts = append(opts.modOpts,
				mod.WithConfigTimestamp(mod.OptTime{
					Set:   t,
					After: t,
				}),
				mod.WithLayerTimestamp(mod.OptTime{
					Set:   t,
					After: t,
				}))
			return nil
		},
	}, "time-max", `max timestamp for both the config and layers`)
	_ = cmd.Flags().MarkHidden("time-max") // TODO: deprecate
	flagDocker := cmd.Flags().VarPF(&modFlagFunc{
		t: "bool",
		f: func(val string) error {
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("unable to parse value %s: %w", val, err)
			}
			if b {
				opts.modOpts = append(opts.modOpts, mod.WithManifestToDocker())
			}
			return nil
		},
	}, "to-docker", "", `convert to Docker schema2 media types`)
	flagDocker.NoOptDefVal = "true"
	flagOCI := cmd.Flags().VarPF(&modFlagFunc{
		t: "bool",
		f: func(val string) error {
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("unable to parse value %s: %w", val, err)
			}
			if b {
				opts.modOpts = append(opts.modOpts, mod.WithManifestToOCI())
			}
			return nil
		},
	}, "to-oci", "", `convert to OCI media types`)
	flagOCI.NoOptDefVal = "true"
	flagOCIReferrers := cmd.Flags().VarPF(&modFlagFunc{
		t: "bool",
		f: func(val string) error {
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("unable to parse value %s: %w", val, err)
			}
			if b {
				opts.modOpts = append(opts.modOpts, mod.WithManifestToOCIReferrers())
			}
			return nil
		},
	}, "to-oci-referrers", "", `convert to OCI referrers`)
	flagOCIReferrers.NoOptDefVal = "true"
	cmd.Flags().Var(&modFlagFunc{
		t: "stringArray",
		f: func(val string) error {
			opts.modOpts = append(opts.modOpts, mod.WithVolumeAdd(val))
			return nil
		},
	}, "volume-add", `add a volume definition`)
	cmd.Flags().Var(&modFlagFunc{
		t: "stringArray",
		f: func(val string) error {
			opts.modOpts = append(opts.modOpts, mod.WithVolumeRm(val))
			return nil
		},
	}, "volume-rm", `delete a volume definition`)

	return cmd
}

func newImageRateLimitCmd(rOpts *rootOpts) *cobra.Command {
	opts := imageOpts{
		rootOpts: rOpts,
	}
	cmd := &cobra.Command{
		Use:     "ratelimit <image_ref>",
		Aliases: []string{"rate-limit"},
		Short:   "show the current rate limit",
		Long: `Shows the rate limit using an http head request against the image manifest.
If Set is false, the Remain value was not provided.
The other values may be 0 if not provided by the registry.`,
		Example: `
# return the current rate limit for pulling the alpine image
regctl image ratelimit alpine

# return the number of pulls remaining
regctl image ratelimit alpine --format '{{.Remain}}'`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: rOpts.completeArgTag,
		RunE:              opts.runImageRateLimit,
	}
	cmd.Flags().StringVar(&opts.format, "format", "{{printPretty .}}", "Format output with go template syntax")
	_ = cmd.RegisterFlagCompletionFunc("format", completeArgNone)
	return cmd
}

func imageParseOptTime(s string) (mod.OptTime, map[string]string, error) {
	ot := mod.OptTime{}
	otherFields := map[string]string{}
	for _, ss := range strings.Split(s, ",") {
		kv := strings.SplitN(ss, "=", 2)
		if len(kv) != 2 {
			return ot, otherFields, fmt.Errorf("parameter without a value: %s", ss)
		}
		switch kv[0] {
		case "set":
			t, err := time.Parse(time.RFC3339, kv[1])
			if err != nil {
				return ot, otherFields, fmt.Errorf("set time must be formatted %s: %w", time.RFC3339, err)
			}
			ot.Set = t
		case "after":
			t, err := time.Parse(time.RFC3339, kv[1])
			if err != nil {
				return ot, otherFields, fmt.Errorf("after time must be formatted %s: %w", time.RFC3339, err)
			}
			ot.After = t
		case "from-label":
			ot.FromLabel = kv[1]
		case "base-ref":
			r, err := ref.New(kv[1])
			if err != nil {
				return ot, otherFields, fmt.Errorf("failed to parse base ref: %w", err)
			}
			ot.BaseRef = r
		case "base-layers":
			i, err := strconv.Atoi(kv[1])
			if err != nil {
				return ot, otherFields, fmt.Errorf("unable to parse base layer count: %w", err)
			}
			ot.BaseLayers = i
		default:
			otherFields[kv[0]] = kv[1]
		}
	}
	return ot, otherFields, nil
}

func (opts *imageOpts) runImageCheckBase(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	r, err := ref.New(args[0])
	if err != nil {
		return err
	}
	rc := opts.rootOpts.newRegClient()
	defer rc.Close(ctx, r)

	rcOpts := []regclient.ImageOpts{}
	if opts.checkBaseDigest != "" {
		rcOpts = append(rcOpts, regclient.ImageWithCheckBaseDigest(opts.checkBaseDigest))
	}
	if opts.checkBaseRef != "" {
		rcOpts = append(rcOpts, regclient.ImageWithCheckBaseRef(opts.checkBaseRef))
	}
	if opts.checkSkipConfig {
		rcOpts = append(rcOpts, regclient.ImageWithCheckSkipConfig())
	}
	if opts.platform != "" {
		rcOpts = append(rcOpts, regclient.ImageWithPlatform(opts.platform))
	}

	err = rc.ImageCheckBase(ctx, r, rcOpts...)
	if err == nil {
		opts.rootOpts.log.Info("base image matches")
		if !opts.quiet {
			fmt.Fprintf(cmd.OutOrStdout(), "base image matches\n")
		}
	} else if errors.Is(err, errs.ErrMismatch) {
		opts.rootOpts.log.Info("base image mismatch",
			slog.String("err", err.Error()))
		// return empty error message
		err = fmt.Errorf("%.0w", err)
		if !opts.quiet {
			fmt.Fprintf(cmd.OutOrStdout(), "base image has changed\n")
		}
	}
	return err
}

func (opts *imageOpts) runImageCopy(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	rSrc, err := ref.New(args[0])
	if err != nil {
		return err
	}
	rTgt, err := ref.New(args[1])
	if err != nil {
		return err
	}
	if (opts.referrerSrc != "" || opts.referrerTgt != "") && !opts.referrers {
		return fmt.Errorf("referrers must be enabled to specify an external referrers source or target%.0w", errs.ErrUnsupported)
	}
	rc := opts.rootOpts.newRegClient()
	defer rc.Close(ctx, rSrc)
	defer rc.Close(ctx, rTgt)
	if opts.platform != "" {
		p, err := platform.Parse(opts.platform)
		if err != nil {
			return err
		}
		m, err := rc.ManifestGet(ctx, rSrc, regclient.WithManifestPlatform(p))
		if err != nil {
			return err
		}
		rSrc = rSrc.AddDigest(m.GetDescriptor().Digest.String())
	}
	opts.rootOpts.log.Debug("Image copy",
		slog.String("source", rSrc.CommonName()),
		slog.String("target", rTgt.CommonName()),
		slog.Bool("recursive", opts.forceRecursive),
		slog.Bool("digest-tags", opts.digestTags))
	rcOpts := []regclient.ImageOpts{}
	if opts.fastCheck {
		rcOpts = append(rcOpts, regclient.ImageWithFastCheck())
	}
	if opts.forceRecursive {
		rcOpts = append(rcOpts, regclient.ImageWithForceRecursive())
	}
	if opts.includeExternal {
		rcOpts = append(rcOpts, regclient.ImageWithIncludeExternal())
	}
	if opts.digestTags {
		rcOpts = append(rcOpts, regclient.ImageWithDigestTags())
	}
	if opts.referrers {
		rcOpts = append(rcOpts, regclient.ImageWithReferrers())
	}
	if opts.referrerSrc != "" {
		referrerSrc, err := ref.New(opts.referrerSrc)
		if err != nil {
			return fmt.Errorf("failed parsing referrer external source: %w", err)
		}
		rcOpts = append(rcOpts, regclient.ImageWithReferrerSrc(referrerSrc))
	}
	if opts.referrerTgt != "" {
		referrerTgt, err := ref.New(opts.referrerTgt)
		if err != nil {
			return fmt.Errorf("failed parsing referrer external target: %w", err)
		}
		rcOpts = append(rcOpts, regclient.ImageWithReferrerTgt(referrerTgt))
	}
	if len(opts.platforms) > 0 {
		rcOpts = append(rcOpts, regclient.ImageWithPlatforms(opts.platforms))
	}
	// check for a tty and attach progress reporter
	done := make(chan bool)
	var progress *imageProgress
	if !flagChanged(cmd, "verbosity") && ascii.IsWriterTerminal(cmd.ErrOrStderr()) {
		progress = &imageProgress{
			start:    time.Now(),
			entries:  map[string]*imageProgressEntry{},
			asciiOut: ascii.NewLines(cmd.ErrOrStderr()),
			bar:      ascii.NewProgressBar(cmd.ErrOrStderr()),
		}
		ticker := time.NewTicker(progressFreq)
		defer ticker.Stop()
		go func() {
			for {
				select {
				case <-done:
					ticker.Stop()
					return
				case <-ticker.C:
					progress.display(false)
				}
			}
		}()
		rcOpts = append(rcOpts, regclient.ImageWithCallback(progress.callback))
	}
	err = rc.ImageCopy(ctx, rSrc, rTgt, rcOpts...)
	if progress != nil {
		close(done)
		progress.display(true)
	}
	if err != nil {
		return err
	}
	if !flagChanged(cmd, "format") {
		opts.format = "{{ .CommonName }}\n"
	}
	return template.Writer(cmd.OutOrStdout(), opts.format, rTgt)
}

type imageProgress struct {
	mu       sync.Mutex
	start    time.Time
	entries  map[string]*imageProgressEntry
	asciiOut *ascii.Lines
	bar      *ascii.ProgressBar
	changed  bool
}

type imageProgressEntry struct {
	kind        types.CallbackKind
	instance    string
	state       types.CallbackState
	start, last time.Time
	cur, total  int64
	bps         []float64
}

func (ip *imageProgress) callback(kind types.CallbackKind, instance string, state types.CallbackState, cur, total int64) {
	// track kind/instance
	ip.mu.Lock()
	defer ip.mu.Unlock()
	ip.changed = true
	now := time.Now()
	if e, ok := ip.entries[kind.String()+":"+instance]; ok {
		e.state = state
		diff := now.Sub(e.last)
		bps := float64(cur-e.cur) / diff.Seconds()
		e.state = state
		e.last = now
		e.cur = cur
		e.total = total
		if len(e.bps) >= 10 {
			e.bps = append(e.bps[1:], bps)
		} else {
			e.bps = append(e.bps, bps)
		}
	} else {
		ip.entries[kind.String()+":"+instance] = &imageProgressEntry{
			kind:     kind,
			instance: instance,
			state:    state,
			start:    now,
			last:     now,
			cur:      cur,
			total:    total,
			bps:      []float64{},
		}
	}
}

func (ip *imageProgress) display(final bool) {
	ip.mu.Lock()
	defer ip.mu.Unlock()
	if !ip.changed && !final {
		return // skip since no changes since last display and not the final display
	}
	var manifestTotal, manifestFinished, sum, skipped, queued int64
	// sort entry keys by start time
	keys := make([]string, 0, len(ip.entries))
	for k := range ip.entries {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		// show finished entries at the top, queued entries on the bottom
		if ip.entries[a].state > ip.entries[b].state {
			return -1
		} else if ip.entries[a].state < ip.entries[b].state {
			return 1
		} else if ip.entries[a].state != types.CallbackActive {
			// sort inactive entries by finish time
			return ip.entries[a].last.Compare(ip.entries[b].last)
		} else {
			// sort bytes sent descending
			return cmp.Compare(ip.entries[a].cur, ip.entries[b].cur) * -1
		}
	})
	startCount, startLimit := 0, 2
	finishedCount, finishedLimit := 0, 2
	// hide old finished entries
	for i := len(keys) - 1; i >= 0; i-- {
		e := ip.entries[keys[i]]
		if e.kind != types.CallbackManifest && e.state == types.CallbackFinished {
			finishedCount++
			if finishedCount > finishedLimit {
				e.state = types.CallbackArchived
			}
		}
	}
	for _, k := range keys {
		e := ip.entries[k]
		switch e.kind {
		case types.CallbackManifest:
			manifestTotal++
			if e.state == types.CallbackFinished || e.state == types.CallbackSkipped {
				manifestFinished++
			}
		default:
			// show progress bars
			if !final && (e.state == types.CallbackActive || (e.state == types.CallbackStarted && startCount < startLimit) || e.state == types.CallbackFinished) {
				if e.state == types.CallbackStarted {
					startCount++
				}
				pre := e.instance + " "
				if len(pre) > 15 {
					pre = pre[:14] + " "
				}
				pct := float64(e.cur) / float64(e.total)
				post := fmt.Sprintf(" %4.2f%% %s/%s", pct*100, units.HumanSize(float64(e.cur)), units.HumanSize(float64(e.total)))
				ip.asciiOut.Add(ip.bar.Generate(pct, pre, post))
			}
			// track stats
			if e.state == types.CallbackSkipped {
				skipped += e.total
			} else if e.total > 0 {
				sum += e.cur
				queued += e.total - e.cur
			}
		}
	}
	// show stats summary
	ip.asciiOut.Add(fmt.Appendf(nil, "Manifests: %d/%d | Blobs: %s copied, %s skipped",
		manifestFinished, manifestTotal,
		units.HumanSize(float64(sum)),
		units.HumanSize(float64(skipped))))
	if queued > 0 {
		ip.asciiOut.Add(fmt.Appendf(nil, ", %s queued", units.HumanSize(float64(queued))))
	}
	ip.asciiOut.Add(fmt.Appendf(nil, " | Elapsed: %ds\n", int64(time.Since(ip.start).Seconds())))
	ip.asciiOut.Flush()
	if !final {
		ip.asciiOut.Return()
	}
}

func (opts *imageOpts) runImageCreate(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// validate media type
	if opts.mediaType != mediatype.OCI1Manifest && opts.mediaType != mediatype.Docker2Manifest {
		return fmt.Errorf("unsupported manifest media type: %s%.0w", opts.mediaType, errs.ErrUnsupportedMediaType)
	}

	// parse ref
	r, err := ref.New(args[0])
	if err != nil {
		return err
	}

	// setup regclient
	rc := opts.rootOpts.newRegClient()
	defer rc.Close(ctx, r)

	// define the image config
	conf := v1.Image{
		Config: v1.ImageConfig{},
		RootFS: v1.RootFS{
			Type:    "layers",
			DiffIDs: []digest.Digest{},
		},
		History: []v1.History{},
	}

	if opts.created == "now" {
		now := time.Now().UTC()
		conf.Created = &now
	} else if opts.created != "" {
		t, err := time.Parse(time.RFC3339, opts.created)
		if err != nil {
			return fmt.Errorf("failed to parse created time %s: %w", opts.created, err)
		}
		conf.Created = &t
	}

	labels := map[string]string{}
	for _, l := range opts.labels {
		lSplit := strings.SplitN(l, "=", 2)
		if len(lSplit) == 1 {
			labels[lSplit[0]] = ""
		} else {
			labels[lSplit[0]] = lSplit[1]
		}
	}
	if len(labels) > 0 {
		conf.Config.Labels = labels
	}

	if opts.platform != "" {
		p, err := platform.Parse(opts.platform)
		if err != nil {
			return fmt.Errorf("failed to parse platform: %w", err)
		}
		conf.Platform = p
	}

	// TODO: add layers

	// push the config
	cJSON, err := json.Marshal(conf)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	cd, err := rc.BlobPut(ctx, r, descriptor.Descriptor{}, bytes.NewReader(cJSON))
	if err != nil {
		return fmt.Errorf("failed to push config: %w", err)
	}

	// parse annotations
	annotations := map[string]string{}
	for _, a := range opts.annotations {
		aSplit := strings.SplitN(a, "=", 2)
		if len(aSplit) == 1 {
			annotations[aSplit[0]] = ""
		} else {
			annotations[aSplit[0]] = aSplit[1]
		}
	}

	// build the manifest
	mOpts := []manifest.Opts{}
	switch opts.mediaType {
	case mediatype.OCI1Manifest:
		cd.MediaType = mediatype.OCI1ImageConfig
		m := v1.Manifest{
			Versioned: v1.ManifestSchemaVersion,
			MediaType: mediatype.OCI1Manifest,
			Config:    cd,
		}
		if len(annotations) > 0 {
			m.Annotations = annotations
		}
		mOpts = append(mOpts, manifest.WithOrig(m))
	case mediatype.Docker2Manifest:
		cd.MediaType = mediatype.Docker2ImageConfig
		m := schema2.Manifest{
			Versioned: schema2.ManifestSchemaVersion,
			Config:    cd,
		}
		if len(annotations) > 0 {
			m.Annotations = annotations
		}
		mOpts = append(mOpts, manifest.WithOrig(m))
	}
	mm, err := manifest.New(mOpts...)
	if err != nil {
		return err
	}

	// push the image
	if opts.byDigest {
		r = r.SetDigest(mm.GetDescriptor().Digest.String())
	}
	err = rc.ManifestPut(ctx, r, mm)
	if err != nil {
		return err
	}

	// format output
	result := struct {
		Manifest manifest.Manifest
	}{
		Manifest: mm,
	}
	if opts.byDigest && opts.format == "" {
		opts.format = "{{ printf \"%s\\n\" .Manifest.GetDescriptor.Digest }}"
	}
	return template.Writer(cmd.OutOrStdout(), opts.format, result)
}

func (opts *imageOpts) runImageExport(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	// dedup warnings
	if w := warning.FromContext(ctx); w == nil {
		ctx = warning.NewContext(ctx, &warning.Warning{Hook: warning.DefaultHook()})
	}
	r, err := ref.New(args[0])
	if err != nil {
		return err
	}
	var w io.Writer
	if len(args) == 2 {
		w, err = os.Create(args[1])
		if err != nil {
			return err
		}
	} else {
		w = cmd.OutOrStdout()
	}
	rc := opts.rootOpts.newRegClient()
	defer rc.Close(ctx, r)
	rcOpts := []regclient.ImageOpts{}
	if opts.platform != "" {
		p, err := platform.Parse(opts.platform)
		if err != nil {
			return err
		}
		m, err := rc.ManifestGet(ctx, r, regclient.WithManifestPlatform(p))
		if err != nil {
			return err
		}
		r = r.AddDigest(m.GetDescriptor().Digest.String())
	}
	if opts.exportCompress {
		rcOpts = append(rcOpts, regclient.ImageWithExportCompress())
	}
	if opts.exportRef != "" {
		eRef, err := ref.New(opts.exportRef)
		if err != nil {
			return fmt.Errorf("cannot parse %s: %w", opts.exportRef, err)
		}
		rcOpts = append(rcOpts, regclient.ImageWithExportRef(eRef))
	}
	opts.rootOpts.log.Debug("Image export",
		slog.String("ref", r.CommonName()))
	return rc.ImageExport(ctx, r, w, rcOpts...)
}

func (opts *imageOpts) runImageGetFile(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	// dedup warnings
	if w := warning.FromContext(ctx); w == nil {
		ctx = warning.NewContext(ctx, &warning.Warning{Hook: warning.DefaultHook()})
	}
	r, err := ref.New(args[0])
	if err != nil {
		return err
	}
	filename := args[1]
	filename = strings.TrimPrefix(filename, "/")
	rc := opts.rootOpts.newRegClient()
	defer rc.Close(ctx, r)

	opts.rootOpts.log.Debug("Get file",
		slog.String("ref", r.CommonName()),
		slog.String("filename", filename))

	if opts.platform == "" {
		opts.platform = "local"
	}
	p, err := platform.Parse(opts.platform)
	if err != nil {
		opts.rootOpts.log.Warn("Could not parse platform",
			slog.String("platform", opts.platform),
			slog.String("err", err.Error()))
	}
	m, err := rc.ManifestGet(ctx, r, regclient.WithManifestPlatform(p))
	if err != nil {
		return err
	}
	// go through layers in reverse
	mi, ok := m.(manifest.Imager)
	if !ok {
		return fmt.Errorf("reference is not a known image media type")
	}
	layers, err := mi.GetLayers()
	if err != nil {
		return err
	}
	for i := len(layers) - 1; i >= 0; i-- {
		blob, err := rc.BlobGet(ctx, r, layers[i])
		if err != nil {
			return fmt.Errorf("failed pulling layer %d: %w", i, err)
		}
		btr, err := blob.ToTarReader()
		if err != nil {
			return fmt.Errorf("could not convert layer %d to tar reader: %w", i, err)
		}
		th, rdr, err := btr.ReadFile(filename)
		if err != nil {
			if errors.Is(err, errs.ErrFileNotFound) {
				if err := btr.Close(); err != nil {
					return err
				}
				if err := blob.Close(); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("failed pulling from layer %d: %w", i, err)
		}
		// file found, output
		if opts.format != "" {
			data := struct {
				Header *tar.Header
				Reader io.Reader
			}{
				Header: th,
				Reader: rdr,
			}
			return template.Writer(cmd.OutOrStdout(), opts.format, data)
		}
		var w io.Writer
		if len(args) < 3 {
			w = cmd.OutOrStdout()
		} else {
			w, err = os.Create(args[2])
			if err != nil {
				return err
			}
		}
		_, err = io.Copy(w, rdr)
		if err != nil {
			return err
		}
		if err := btr.Close(); err != nil {
			return err
		}
		if err := blob.Close(); err != nil {
			return err
		}
		return nil
	}
	// all layers exhausted, not found or deleted
	return errs.ErrNotFound
}

func (opts *imageOpts) runImageImport(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	r, err := ref.New(args[0])
	if err != nil {
		return err
	}
	rcOpts := []regclient.ImageOpts{}
	if opts.importName != "" {
		rcOpts = append(rcOpts, regclient.ImageWithImportName(opts.importName))
	}
	rs, err := os.Open(args[1])
	if err != nil {
		return err
	}
	defer rs.Close()
	rc := opts.rootOpts.newRegClient()
	defer rc.Close(ctx, r)
	opts.rootOpts.log.Debug("Image import",
		slog.String("ref", r.CommonName()),
		slog.String("file", args[1]))

	return rc.ImageImport(ctx, r, rs, rcOpts...)
}

func (opts *imageOpts) runImageInspect(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	r, err := ref.New(args[0])
	if err != nil {
		return err
	}
	rc := opts.rootOpts.newRegClient()
	defer rc.Close(ctx, r)

	opts.rootOpts.log.Debug("Image inspect",
		slog.String("host", r.Registry),
		slog.String("repo", r.Repository),
		slog.String("tag", r.Tag),
		slog.String("platform", opts.platform))

	rcOpts := []regclient.ImageOpts{}
	if opts.platform != "" {
		rcOpts = append(rcOpts, regclient.ImageWithPlatform(opts.platform))
	}
	blobConfig, err := rc.ImageConfig(ctx, r, rcOpts...)
	if err != nil {
		if errors.Is(err, errs.ErrUnsupportedMediaType) {
			err = fmt.Errorf("artifacts are not supported with \"regctl image inspect\", use \"regctl artifact get --config\" instead: %w", err)
		}
		return err
	}
	result := struct {
		*blob.BOCIConfig
		v1.Image
	}{
		BOCIConfig: blobConfig,
		Image:      blobConfig.GetConfig(),
	}
	switch opts.format {
	case "raw":
		opts.format = "{{ range $key,$vals := .RawHeaders}}{{range $val := $vals}}{{printf \"%s: %s\\n\" $key $val }}{{end}}{{end}}{{printf \"\\n%s\" .RawBody}}"
	case "rawBody", "raw-body", "body":
		opts.format = "{{printf \"%s\" .RawBody}}"
	case "rawHeaders", "raw-headers", "headers":
		opts.format = "{{ range $key,$vals := .RawHeaders}}{{range $val := $vals}}{{printf \"%s: %s\\n\" $key $val }}{{end}}{{end}}"
	}
	return template.Writer(cmd.OutOrStdout(), opts.format, result)
}

func (opts *imageOpts) runImageMod(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	rSrc, err := ref.New(args[0])
	if err != nil {
		return err
	}
	var rTgt ref.Ref
	if opts.create != "" {
		if strings.ContainsAny(opts.create, "/:") {
			rTgt, err = ref.New((opts.create))
			if err != nil {
				return fmt.Errorf("failed to parse new image name %s: %w", opts.create, err)
			}
		} else {
			rTgt = rSrc.SetTag(opts.create)
		}
	} else if opts.replace {
		rTgt = rSrc
	} else {
		rTgt = rSrc.SetTag("")
	}
	opts.modOpts = append(opts.modOpts, mod.WithRefTgt(rTgt))
	rc := opts.rootOpts.newRegClient()

	opts.rootOpts.log.Debug("Modifying image",
		slog.String("ref", rSrc.CommonName()))

	defer rc.Close(ctx, rSrc)
	rOut, err := mod.Apply(ctx, rc, rSrc, opts.modOpts...)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", rOut.CommonName())
	err = rc.Close(ctx, rOut)
	if err != nil {
		return fmt.Errorf("failed to close ref: %w", err)
	}
	return nil
}

func (opts *imageOpts) runImageRateLimit(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	r, err := ref.New(args[0])
	if err != nil {
		return err
	}
	rc := opts.rootOpts.newRegClient()

	opts.rootOpts.log.Debug("Image rate limit",
		slog.String("host", r.Registry),
		slog.String("repo", r.Repository),
		slog.String("tag", r.Tag))

	// request only the headers, avoids adding to Docker Hub rate limits
	m, err := rc.ManifestHead(ctx, r)
	if err != nil {
		return err
	}

	return template.Writer(cmd.OutOrStdout(), opts.format, manifest.GetRateLimit(m))
}

type modFlagFunc struct {
	f func(string) error
	t string
}

func (m *modFlagFunc) IsBoolFlag() bool {
	return m.t == "bool"
}

func (m *modFlagFunc) String() string {
	return ""
}

func (m *modFlagFunc) Set(val string) error {
	return m.f(val)
}

func (m *modFlagFunc) Type() string {
	return m.t
}
