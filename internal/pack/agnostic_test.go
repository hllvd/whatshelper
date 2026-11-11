package pack_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCoreAgnosticism_NoVerticalTermsInInternal(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)\b(imóvel|imovel|imóveis|imoveis|corretor|corretora|visita|visit|broker|property|bedrooms|apartamento|quartos)\b`)

	internalRoot := filepath.Join("..")
	err := filepath.WalkDir(internalRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || d.Name() == "agnostic_test.go" {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if match := forbidden.FindString(string(data)); match != "" {
			t.Errorf("termo específico de vertical (%q) encontrado em %s; o core deve ser agnóstico de mercado", match, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("erro ao percorrer internal/: %v", err)
	}
}
