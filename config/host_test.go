package config

import (
	"encoding/json"
	"testing"
)

func TestConfig(t *testing.T) {
	// generate new/blank
	blankHostP := HostNew()

	// generate new/hostname
	emptyHostP := HostNewName("host.example.org")

	// parse json
	exJSON := `
	{
	  "tls": "enabled",
		"hostname": "host.example.com",
		"user": "user-ex",
		"pass": "secret",
		"pathPrefix": "hub",
		"mirrors": ["host1.example.com","host2.example.com"],
		"priority": 42,
		"apiOpts": {"disableHead": "true"},
		"blobChunk": 123456,
		"blobMax": 999999
	}
	`
	exJSON2 := `
	{
	  "tls": "disabled",
		"hostname": "host2.example.com",
		"user": "user-ex3",
		"pass": "secret3",
		"pathPrefix": "hub3",
		"mirrors": ["host3.example.com"],
		"priority": 42,
		"apiOpts": {"disableHead": "false", "unknownOpt": "3"},
		"blobChunk": 333333,
		"blobMax": 333333
	}
	`
	var exHost, exHost2 Host
	err := json.Unmarshal([]byte(exJSON), &exHost)
	if err != nil {
		t.Errorf("failed unmarshaling exJson: %v", err)
	}
	err = json.Unmarshal([]byte(exJSON2), &exHost2)
	if err != nil {
		t.Errorf("failed unmarshaling exJson2: %v", err)
	}

	// merge blank with json
	exMergeBlank := *blankHostP
	err = (&exMergeBlank).Merge(exHost, nil)
	if err != nil {
		t.Errorf("failed to merge blank host with exHost: %v", err)
	}
	exMergeHost2 := exHost
	err = (&exMergeHost2).Merge(exHost2, nil)
	if err != nil {
		t.Errorf("failed to merge ex host with exHost2: %v", err)
	}

	// verify fields in each
	tests := []struct {
		name       string
		host       Host
		hostExpect Host
	}{
		{
			name: "blank",
			host: *blankHostP,
			hostExpect: Host{
				TLS:     TLSEnabled,
				APIOpts: map[string]string{},
			},
		},
		{
			name: "empty",
			host: *emptyHostP,
			hostExpect: Host{
				TLS:      TLSEnabled,
				Hostname: "host.example.org",
				APIOpts:  map[string]string{},
			},
		},
		{
			name: "exHost",
			host: exHost,
			hostExpect: Host{
				TLS:        TLSEnabled,
				Hostname:   "host.example.com",
				User:       "user-ex",
				Pass:       "secret",
				Priority:   42,
				BlobChunk:  123456,
				BlobMax:    999999,
				APIOpts:    map[string]string{"disableHead": "true"},
				PathPrefix: "hub",
				Mirrors:    []string{"host1.example.com", "host2.example.com"},
			},
		},
		{
			name: "exHost2",
			host: exHost2,
			hostExpect: Host{
				TLS:        TLSDisabled,
				Hostname:   "host2.example.com",
				User:       "user-ex3",
				Pass:       "secret3",
				PathPrefix: "hub3",
				Mirrors:    []string{"host3.example.com"},
				Priority:   42,
				APIOpts:    map[string]string{"disableHead": "false", "unknownOpt": "3"},
				BlobChunk:  333333,
				BlobMax:    333333,
			},
		},
		{
			name: "mergeBlank",
			host: exMergeBlank,
			hostExpect: Host{
				TLS:        TLSEnabled,
				Hostname:   "host.example.com",
				User:       "user-ex",
				Pass:       "secret",
				Priority:   42,
				BlobChunk:  123456,
				BlobMax:    999999,
				APIOpts:    map[string]string{"disableHead": "true"},
				PathPrefix: "hub",
				Mirrors:    []string{"host1.example.com", "host2.example.com"},
			},
		},
		{
			name: "mergeHost2",
			host: exMergeHost2,
			hostExpect: Host{
				TLS:        TLSDisabled,
				Hostname:   "host2.example.com",
				User:       "user-ex3",
				Pass:       "secret3",
				PathPrefix: "hub3",
				Mirrors:    []string{"host3.example.com"},
				Priority:   42,
				APIOpts:    map[string]string{"disableHead": "false", "unknownOpt": "3"},
				BlobChunk:  333333,
				BlobMax:    333333,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// check each field
			if tt.host.TLS != tt.hostExpect.TLS {
				expect, _ := tt.hostExpect.TLS.MarshalText()
				found, _ := tt.host.TLS.MarshalText()
				t.Errorf("tls field mismatch, expected %s, found %s", expect, found)
			}
			if tt.host.RegCert != tt.hostExpect.RegCert {
				t.Errorf("regCert field mismatch, expected %s, found %s", tt.hostExpect.RegCert, tt.host.RegCert)
			}
			if tt.host.Hostname != tt.hostExpect.Hostname {
				t.Errorf("hostname field mismatch, expected %s, found %s", tt.hostExpect.Hostname, tt.host.Hostname)
			}
			if tt.host.User != tt.hostExpect.User {
				t.Errorf("user field mismatch, expected %s, found %s", tt.hostExpect.User, tt.host.User)
			}
			if tt.host.Pass != tt.hostExpect.Pass {
				t.Errorf("pass field mismatch, expected %s, found %s", tt.hostExpect.Pass, tt.host.Pass)
			}
			if tt.host.Token != tt.hostExpect.Token {
				t.Errorf("token field mismatch, expected %s, found %s", tt.hostExpect.Token, tt.host.Token)
			}
			if tt.host.PathPrefix != tt.hostExpect.PathPrefix {
				t.Errorf("pathPrefix field mismatch, expected %s, found %s", tt.hostExpect.PathPrefix, tt.host.PathPrefix)
			}
			if tt.host.Priority != tt.hostExpect.Priority {
				t.Errorf("priority field mismatch, expected %d, found %d", tt.hostExpect.Priority, tt.host.Priority)
			}
			if tt.host.BlobChunk != tt.hostExpect.BlobChunk {
				t.Errorf("blobChunk field mismatch, expected %d, found %d", tt.hostExpect.BlobChunk, tt.host.BlobChunk)
			}
			if tt.host.BlobMax != tt.hostExpect.BlobMax {
				t.Errorf("blobMax field mismatch, expected %d, found %d", tt.hostExpect.BlobMax, tt.host.BlobMax)
			}
			if len(tt.host.Mirrors) != len(tt.hostExpect.Mirrors) {
				t.Errorf("mirrors length mismatch, expected %v, found %v", tt.hostExpect.Mirrors, tt.host.Mirrors)
			} else {
				for i := range tt.host.Mirrors {
					if tt.host.Mirrors[i] != tt.hostExpect.Mirrors[i] {
						t.Errorf("mirrors field %d mismatch, expected %s, found %s", i, tt.hostExpect.Mirrors[i], tt.host.Mirrors[i])
					}
				}
			}
			if len(tt.host.APIOpts) != len(tt.hostExpect.APIOpts) {
				t.Errorf("apiOpts length mismatch, expected %v, found %v", tt.hostExpect.APIOpts, tt.host.APIOpts)
			} else {
				for i := range tt.host.APIOpts {
					if tt.host.APIOpts[i] != tt.hostExpect.APIOpts[i] {
						t.Errorf("apiOpts field %s mismatch, expected %s, found %s", i, tt.hostExpect.APIOpts[i], tt.host.APIOpts[i])
					}
				}
			}
		})
	}

}
