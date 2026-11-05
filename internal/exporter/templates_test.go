package exporter

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCustomTemplateRoundTripMetadataAndCatalogue(t *testing.T) {
	root := t.TempDir()
	id := strings.Repeat("a", 32)
	data := testDOCX(t, `<w:p><w:r><w:t>{{title}}：{{project_name}}</w:t></w:r></w:p>`, nil)
	created, err := SaveTemplate(root, id, "现场记录模板", data)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != id || created.Name != "现场记录模板" || created.SHA256 != Digest(data) || created.Format != "docx" {
		t.Fatal("invalid saved metadata", created)
	}
	meta, loaded, err := LoadTemplate(root, id)
	if err != nil || !bytes.Equal(loaded, data) || meta.SHA256 != Digest(data) {
		t.Fatal("template not preserved byte-for-byte", meta, err)
	}
	if len(meta.Fields) != 2 || meta.Fields[0] != "project_name" || meta.Fields[1] != "title" {
		t.Fatal("placeholder inventory incorrect", meta.Fields)
	}
	templates, err := ListTemplates(root)
	if err != nil || len(templates) != 3 {
		t.Fatal("catalogue should contain builtins and saved template", templates, err)
	}
	found := false
	for _, entry := range templates {
		if entry.ID == id {
			found = true
			if entry.Name != created.Name || entry.SHA256 != created.SHA256 {
				t.Fatal("catalogue metadata changed", entry)
			}
		}
	}
	if !found {
		t.Fatal("custom template missing from catalogue")
	}
}
func TestTemplateIDIsImmutableAndCannotOverwritePriorEvidence(t *testing.T) {
	root := t.TempDir()
	id := strings.Repeat("b", 32)
	original := testDOCX(t, `<w:p><w:r><w:t>原模板 {{title}}</w:t></w:r></w:p>`, nil)
	replacement := testDOCX(t, `<w:p><w:r><w:t>替换模板 {{content}}</w:t></w:r></w:p>`, nil)
	meta, err := SaveTemplate(root, id, "原名称", original)
	if err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(root, "templates", "docx", id+".json")
	before, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = SaveTemplate(root, id, "不应替换的名称", replacement); err == nil {
		t.Fatal("same template ID overwrote immutable template")
	}
	after, err := os.ReadFile(metadataPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed replacement changed historical template metadata")
	}
	loaded, data, err := LoadTemplate(root, id)
	if err != nil || !bytes.Equal(data, original) || loaded.SHA256 != meta.SHA256 || loaded.Name != "原名称" {
		t.Fatal("failed replacement changed original template", loaded, err)
	}
}
func TestConcurrentTemplateCreationHasOneConsistentWinner(t *testing.T) {
	root := t.TempDir()
	id := strings.Repeat("c", 32)
	data := [][]byte{testDOCX(t, `<w:p><w:r><w:t>甲 {{title}}</w:t></w:r></w:p>`, nil), testDOCX(t, `<w:p><w:r><w:t>乙 {{content}}</w:t></w:r></w:p>`, nil)}
	start := make(chan struct{})
	results := make([]error, 2)
	var wg sync.WaitGroup
	for i := range data {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, results[index] = SaveTemplate(root, id, []string{"模板甲", "模板乙"}[index], data[index])
		}(i)
	}
	close(start)
	wg.Wait()
	winner := -1
	for i, err := range results {
		if err == nil {
			if winner != -1 {
				t.Fatal("two concurrent writes both claimed same immutable ID")
			}
			winner = i
		}
	}
	if winner < 0 {
		t.Fatal("neither concurrent template creation succeeded", results)
	}
	meta, loaded, err := LoadTemplate(root, id)
	if err != nil || !bytes.Equal(loaded, data[winner]) || meta.SHA256 != Digest(data[winner]) || meta.Name != []string{"模板甲", "模板乙"}[winner] {
		t.Fatal("concurrent winner has mismatched template bytes/metadata", meta, err)
	}
}
func TestTemplateMetadataCollisionCannotClobberOrLeavePartialDocument(t *testing.T) {
	root := t.TempDir()
	id := strings.Repeat("d", 32)
	dir := filepath.Join(root, "templates", "docx")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(dir, id+".json")
	existing := []byte(`{"reserved":"original metadata must survive"}`)
	if err := os.WriteFile(metaPath, existing, 0600); err != nil {
		t.Fatal(err)
	}
	data := testDOCX(t, `<w:p><w:r><w:t>{{title}}</w:t></w:r></w:p>`, nil)
	if _, err := SaveTemplate(root, id, "不能覆盖保留ID", data); err == nil {
		t.Fatal("metadata collision unexpectedly succeeded")
	}
	after, err := os.ReadFile(metaPath)
	if err != nil || !bytes.Equal(after, existing) {
		t.Fatal("preexisting metadata was overwritten")
	}
	if _, err = os.Stat(filepath.Join(dir, id+".docx")); !os.IsNotExist(err) {
		t.Fatalf("failed save left a partial document: %v", err)
	}
}
func TestRejectInvalidTemplateBeforeSavingAndDetectTampering(t *testing.T) {
	root := t.TempDir()
	valid := testDOCX(t, `<w:p><w:r><w:t>{{title}}</w:t></w:r></w:p>`, nil)
	invalid := testDOCX(t, `<w:p><w:r><w:t>{{unknown}}</w:t></w:r></w:p>`, nil)
	id := strings.Repeat("e", 32)
	if _, err := SaveTemplate(root, id, "坏模板", invalid); err == nil {
		t.Fatal("unknown placeholder was saved")
	}
	if _, err := os.Stat(filepath.Join(root, "templates", "docx", id+".docx")); !os.IsNotExist(err) {
		t.Fatal("invalid template wrote a document")
	}
	for _, unsafe := range []string{"../escape", "builtin-journal", strings.Repeat("A", 32), "short"} {
		if _, err := SaveTemplate(root, unsafe, "不安全ID", valid); err == nil {
			t.Fatalf("unsafe template ID accepted: %s", unsafe)
		}
	}
	if _, err := SaveTemplate(root, id, "完整模板", valid); err != nil {
		t.Fatal(err)
	}
	other := testDOCX(t, `<w:p><w:r><w:t>篡改后 {{content}}</w:t></w:r></w:p>`, nil)
	if err := os.WriteFile(filepath.Join(root, "templates", "docx", id+".docx"), other, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadTemplate(root, id); err == nil {
		t.Fatal("tampered template passed metadata hash check")
	}
}
