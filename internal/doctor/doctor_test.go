package doctor

import (
	"context"
	"fmt"
	"forge/internal/project"
	"forge/internal/runner"
	"testing"
)

type fakeRunner struct{ versions map[string]string }

func (f fakeRunner) Run(_ context.Context, s runner.Spec) error {
	v, ok := f.versions[s.Command]
	if !ok {
		return fmt.Errorf("not found")
	}
	fmt.Fprintln(s.Stdout, v)
	return nil
}
func TestCreationRequiresOnlyGeneratorTools(t *testing.T) {
	o := project.Defaults()
	o.Name = "orders"
	o.Frontend = "none"
	r := Check(context.Background(), fakeRunner{}, project.New(o), true)
	if r.Status != "success" {
		t.Fatalf("HTTP-only generation required build tools: %+v", r)
	}
	if r.Tools["docker"].Required || r.Tools["java"].Required {
		t.Fatal("irrelevant creation dependency")
	}
}
func TestDoctorReportsMissingAndUnsupportedRequiredTools(t *testing.T) {
	o := project.Defaults()
	o.Name = "orders"
	o.Backend = "none"
	c := project.New(o)
	f := fakeRunner{map[string]string{"node": "v18.20.0", "npm": "10.0.0", "npx": "10.0.0"}}
	r := Check(context.Background(), f, c, true)
	if r.Status != "error" || r.Tools["node"].Compatible {
		t.Fatalf("accepted incompatible Node: %+v", r)
	}
	f.versions["node"] = "v22.1.0"
	r = Check(context.Background(), f, c, true)
	if r.Status != "success" {
		t.Fatalf("compatible generator environment: %+v", r)
	}
	delete(f.versions, "npx")
	if Check(context.Background(), f, c, true).Status != "error" {
		t.Fatal("missing npx accepted")
	}
}
func TestInventoryMissingOptionalToolsDoesNotFail(t *testing.T) {
	r := Check(context.Background(), fakeRunner{}, nil, false)
	if r.Status != "success" || len(r.Tools) != 8 {
		t.Fatalf("bad inventory %+v", r)
	}
}
func TestJavaVersionParsing(t *testing.T) {
	o := project.Defaults()
	o.Name = "orders"
	o.Frontend = "none"
	c := project.New(o)
	f := fakeRunner{map[string]string{"java": "openjdk version \"17.0.12\"", "mvn": "Apache Maven 3.9.9"}}
	r := Check(context.Background(), f, c, false)
	if r.Tools["java"].Compatible || r.Status != "error" {
		t.Fatal("Java mismatch accepted")
	}
	f.versions["java"] = "openjdk version \"21.0.6\""
	if Check(context.Background(), f, c, false).Status != "success" {
		t.Fatal("Java21 rejected")
	}
}

func TestJavaOptionsBannerDoesNotOverrideVersion(t *testing.T) {
	o := project.Defaults()
	o.Name = "orders"
	o.Frontend = "none"
	f := fakeRunner{map[string]string{"java": "Picked up JAVA_TOOL_OPTIONS: -Xmx512m\nopenjdk version \"21.0.8\"", "mvn": "Apache Maven 3.9.9"}}
	r := Check(context.Background(), f, project.New(o), false)
	if r.Status != "success" {
		t.Fatalf("Java banner mistaken for version %+v", r)
	}
}
