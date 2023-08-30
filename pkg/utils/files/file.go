package files

import (
	"os"
	"path/filepath"
	"strings"
)

func ReadFiles(ps string) (docs map[string]string, err error) {
	var p os.FileInfo
	docs = map[string]string{}
	p, err = os.Stat(ps)
	if err != nil {
		return
	}
	if p.IsDir() {
		var subFiles []os.DirEntry
		subFiles, err = os.ReadDir(ps)
		if err != nil {
			return
		}
		for _, subFile := range subFiles {
			subDocs := make(map[string]string)
			subDocs, err = ReadFiles(filepath.Join(ps, subFile.Name()))
			if err != nil {
				return
			}
			for k, v := range subDocs {
				docs[k] = v
			}
		}
		return
	}
	if !strings.HasSuffix(p.Name(), ".md") || !strings.HasSuffix(p.Name(), ".txt") {
		return
	}
	doc, err := os.ReadFile(ps)
	if err != nil {
		return
	}
	docs[ps] = string(doc)
	return
}
