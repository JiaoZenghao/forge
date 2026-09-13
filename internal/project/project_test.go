package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionsRejectUnsafeNamesAndUnsupportedSelections(t *testing.T) {
	for _, name := range []string{"", "../escape", "a/b", `a\b`, "CON", "con", "aux", "nul", "com1", "lpt9", "bad name", "a.", "a&whoami", "a%PATH%", "-flag", "Camel"} {
		t.Run(name, func(t *testing.T) {
			o := Defaults()
			o.Name = name
			if o.Validate() == nil {
				t.Fatalf("accepted unsafe name %q", name)
			}
		})
	}
	o := Defaults()
	o.Name = "order-system"
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	o.Java = 17
	if o.Validate() == nil {
		t.Fatal("accepted unsupported Java")
	}
	o = Defaults()
	o.Name = "app"
	o.Frontend = "none"
	o.Backend = "none"
	if o.Validate() == nil {
		t.Fatal("accepted empty project")
	}
	o.Python = "uv"
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	o.CI = "jenkins"
	if o.Validate() == nil {
		t.Fatal("accepted unknown provider")
	}
}
func TestMetadataRoundTripAndRefuseOverwrite(t *testing.T) {
	root := t.TempDir()
	o := Defaults()
	o.Name = "orders"
	c := New(o)
	c.ForgeVersion = "test"
	c.ManagedFiles = []string{"README.md"}
	if err := Save(root, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Project != "orders" || loaded.Backend.Java != 21 || loaded.Frontend.Path != "frontend" {
		t.Fatalf("bad metadata: %+v", loaded)
	}
	if err := Save(root, c); err == nil {
		t.Fatal("overwrote metadata")
	}
}
func TestStrictMetadataAndPaths(t *testing.T) {
	for _, mutation := range []func(string) string{
		func(s string) string { return s + "surprise: true\n" },
		func(s string) string { return strings.Replace(s, "schemaVersion: 1", "schemaVersion: 99", 1) },
		func(s string) string { return s + "project: duplicate\n" },
		func(s string) string { return strings.Replace(s, "path: frontend", "path: ../escape", 1) },
		func(s string) string { return s + "---\nproject: another\n" },
	} {
		root := t.TempDir()
		o := Defaults()
		o.Name = "orders"
		if err := Save(root, New(o)); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(root, "forge.yaml")
		b, _ := os.ReadFile(p)
		if err := os.WriteFile(p, []byte(mutation(string(b))), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); err == nil {
			t.Fatalf("accepted invalid metadata: %s", mutation(string(b)))
		}
	}
}
func TestMetadataRejectsOverlappingAndUnsafeManagedPaths(t *testing.T) {
	o := Defaults()
	o.Name = "orders"
	for _, p := range []string{"../x", "/x", `C:\x`, `a\b`, "frontend/../x", ".", "a//b", "con", "a:stream"} {
		c := New(o)
		c.Frontend.Path = p
		if c.Validate() == nil {
			t.Errorf("accepted path %q", p)
		}
	}
	c := New(o)
	c.Backend.Path = "frontend/sub"
	if c.Validate() == nil {
		t.Fatal("accepted nested components")
	}
	c = New(o)
	c.ManagedFiles = []string{"../x"}
	if c.Validate() == nil {
		t.Fatal("accepted escaping managed file")
	}
}

func TestMetadataCannotTurnComponentPathsIntoCommands(t *testing.T) {
	o := Defaults()
	o.Name = "orders"
	for _, p := range []string{"--help", "front end", "front;id", "front$(id)", "frontend#comment"} {
		c := New(o)
		c.Frontend.Path = p
		if c.Validate() == nil {
			t.Errorf("accepted shell/CLI-unsafe component path %q", p)
		}
	}
}
