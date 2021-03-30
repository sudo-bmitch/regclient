package main

import (
	"context"
	"io"
	"os"

	"github.com/regclient/regclient/pkg/template"
	"github.com/regclient/regclient/regclient"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var blobCmd = &cobra.Command{
	Use:     "blob <cmd>",
	Aliases: []string{"layer"},
	Short:   "manage image blobs/layers",
}
var blobGetCmd = &cobra.Command{
	Use:     "get <repository> <digest>",
	Aliases: []string{"pull"},
	Short:   "download a blob/layer",
	Long: `Download a blob from the registry. The output is the blob itself which may
be a compressed tar file, a json config, or any other blob supported by the
registry. The blob or layer digest can be found in the image manifest.`,
	Args: cobra.RangeArgs(2, 2),
	RunE: runBlobGet,
}

var blobOpts struct {
	format string
	mt     string
}

func init() {
	blobGetCmd.Flags().StringVarP(&blobOpts.format, "format", "", "{{printPretty .}}", "Format output with go template syntax")
	blobGetCmd.Flags().StringVarP(&blobOpts.mt, "media-type", "", "", "Set the requested mediaType")

	blobCmd.AddCommand(blobGetCmd)
	rootCmd.AddCommand(blobCmd)
}

func runBlobGet(cmd *cobra.Command, args []string) error {
	ref, err := regclient.NewRef(args[0])
	if err != nil {
		return err
	}
	rc := newRegClient()
	accepts := []string{}
	if blobOpts.mt != "" {
		accepts = []string{blobOpts.mt}
	}

	log.WithFields(logrus.Fields{
		"host":       ref.Registry,
		"repository": ref.Repository,
		"digest":     args[1],
	}).Debug("Pulling blob")
	blob, err := rc.BlobGet(context.Background(), ref, args[1], accepts)
	if err != nil {
		return err
	}

	switch blobOpts.format {
	case "raw":
		blobOpts.format = "{{ range $key,$vals := .RawHeaders}}{{range $val := $vals}}{{printf \"%s: %s\\n\" $key $val }}{{end}}{{end}}{{printf \"\\n%s\" .RawBody}}"
	case "rawBody", "raw-body", "body":
		_, err = io.Copy(os.Stdout, blob)
		return err
	case "rawHeaders", "raw-headers", "headers":
		blobOpts.format = "{{ range $key,$vals := .RawHeaders}}{{range $val := $vals}}{{printf \"%s: %s\\n\" $key $val }}{{end}}{{end}}"
	case "{{printPretty .}}":
		_, err = io.Copy(os.Stdout, blob)
		return err
	}

	return template.Writer(os.Stdout, blobOpts.format, blob, template.WithFuncs(regclient.TemplateFuncs))
}