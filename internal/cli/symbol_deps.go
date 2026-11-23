package cli

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"io"
	"strings"
)

// appPackageDeps reads the dependencies an AL .app package declares in its own
// NavxManifest.xml. A .app file is a NAVX header (magic "NAVX", then a uint32
// header length, 40 today) followed by a zip archive that holds the manifest;
// signed packages carry trailing signature bytes after the zip.
//
// ok=false means the bytes are not a readable .app package. The caller treats
// that as "no further dependencies" rather than an error, so a package the CLI
// cannot parse never blocks a download that would have succeeded before #1000.
func appPackageDeps(data []byte) (deps []appDep, ok bool) {
	if len(data) < 8 || string(data[:4]) != "NAVX" {
		return nil, false
	}
	hdr := int(binary.LittleEndian.Uint32(data[4:8]))
	if hdr < 8 || hdr >= len(data) {
		return nil, false
	}
	body := data[hdr:]
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, false
	}
	for _, f := range zr.File {
		if !strings.EqualFold(f.Name, "NavxManifest.xml") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, false
		}
		raw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, false
		}
		return parseNavxDependencies(raw)
	}
	return nil, false
}

// parseNavxDependencies extracts <Dependencies><Dependency .../></Dependencies>
// from a NavxManifest.xml. Matching is on local names, so the manifest's XML
// namespace does not matter. MinVersion is the modern attribute; Version is the
// older spelling.
func parseNavxDependencies(raw []byte) ([]appDep, bool) {
	var pkg struct {
		Dependencies struct {
			Items []struct {
				Name       string `xml:"Name,attr"`
				Publisher  string `xml:"Publisher,attr"`
				MinVersion string `xml:"MinVersion,attr"`
				Version    string `xml:"Version,attr"`
			} `xml:"Dependency"`
		} `xml:"Dependencies"`
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if err := xml.Unmarshal(raw, &pkg); err != nil {
		return nil, false
	}
	var out []appDep
	for _, d := range pkg.Dependencies.Items {
		v := d.MinVersion
		if v == "" {
			v = d.Version
		}
		out = append(out, appDep{Name: d.Name, Publisher: d.Publisher, Version: v})
	}
	return out, true
}

// symbolKey dedupes symbol targets on publisher and name: BC holds one version
// of an app per env, so a second request for the same app at another version
// would fetch the same package twice.
func symbolKey(publisher, name string) string {
	return strings.ToLower(publisher) + "\x00" + strings.ToLower(name)
}
