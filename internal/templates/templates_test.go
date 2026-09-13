package templates

import (
	"strings"
	"testing"

	"forge/internal/project"
	"gopkg.in/yaml.v3"
)

func TestRenderGitLabComponentIsolationAndArtifactFlow(t *testing.T) {
	c := config("gitlab", true, true, true, true)
	files, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"README.md", "AGENTS.md", ".gitignore", "docs/.gitkeep", "scripts/.gitkeep", "deploy/.gitkeep", ".gitlab-ci.yml", ".gitlab/frontend-ci.yml", ".gitlab/backend-ci.yml", ".gitlab/python-ci.yml", "frontend/Dockerfile", "backend/Dockerfile", "services/ai/Dockerfile"}
	for _, name := range want {
		if _, ok := files[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}

	root := decode(t, files[".gitlab-ci.yml"])
	includes := root["include"].([]any)
	if len(includes) != 3 {
		t.Fatalf("include count = %d", len(includes))
	}
	assertChanges(t, includes[0].(map[string]any)["rules"], "frontend/**", ".gitlab/frontend-ci.yml", ".gitlab-ci.yml")
	assertNoChange(t, includes[0].(map[string]any)["rules"], "backend/**")
	assertChanges(t, includes[1].(map[string]any)["rules"], "backend/**", ".gitlab/backend-ci.yml", ".gitlab-ci.yml")
	assertNoChange(t, includes[1].(map[string]any)["rules"], "frontend/**")

	backend := decode(t, files[".gitlab/backend-ci.yml"])
	image := backend["backend:image"].(map[string]any)
	if got := image["needs"].([]any)[0].(map[string]any)["job"]; got != "backend:package" {
		t.Fatalf("image needs %v", got)
	}
	if script := strings.Join(stringsOf(image["script"]), "\n"); strings.Contains(script, "mvn") {
		t.Fatalf("image recompiles Maven artifact: %s", script)
	}
	if rules := image["rules"].([]any); !hasChangesRule(rules) {
		t.Fatalf("image build is not path-gated for all component pipelines: %#v", rules)
	}
	imageScript := strings.Join(stringsOf(image["script"]), "\n")
	if !strings.Contains(imageScript, "PUSH=false") || !strings.Contains(imageScript, `CI_PIPELINE_SOURCE" = "push`) || !strings.Contains(imageScript, `CI_COMMIT_BRANCH" = "$CI_DEFAULT_BRANCH`) || !strings.Contains(imageScript, "push=$PUSH") {
		t.Fatalf("image build/push guard missing: %s", imageScript)
	}
	if !strings.Contains(imageScript, "CI_REGISTRY_PASSWORD") {
		t.Fatalf("guarded registry auth missing: %s", imageScript)
	}
	if s := string(files["backend/Dockerfile"]); strings.Contains(s, "mvn") || !strings.Contains(s, "COPY target/*.jar app.jar") {
		t.Fatalf("backend Dockerfile must consume prebuilt jar: %s", s)
	}
	pack := backend["backend:package"].(map[string]any)
	if got := strings.Join(stringsOf(pack["artifacts"].(map[string]any)["paths"]), "\n"); !strings.Contains(got, "backend/target/*.jar") {
		t.Fatalf("package artifact = %s", got)
	}
	if vars := pack["variables"].(map[string]any); !strings.Contains(vars["MAVEN_OPTS"].(string), ".m2/repository") {
		t.Fatalf("MAVEN_OPTS = %#v", vars)
	}
	if ignored := string(files["backend/.dockerignore"]); strings.Contains(ignored, "target") {
		t.Fatalf("backend dockerignore drops packaged artifact: %s", ignored)
	}

	frontend := decode(t, files[".gitlab/frontend-ci.yml"])
	if _, ok := frontend["frontend:build"]; ok {
		t.Fatal("Docker mode must not create a second frontend production build job")
	}
	if script := strings.Join(stringsOf(frontend["frontend:image"].(map[string]any)["script"]), "\n"); !strings.Contains(script, "buildctl-daemonless.sh") {
		t.Fatalf("rootless BuildKit missing: %s", script)
	}
	for _, tc := range []struct{ file, job string }{{".gitlab/frontend-ci.yml", "frontend:image"}, {".gitlab/backend-ci.yml", "backend:image"}, {".gitlab/python-ci.yml", "python:image"}} {
		ci := decode(t, files[tc.file])
		assertGitLabImageGuard(t, ci[tc.job].(map[string]any))
	}
}

func TestRenderNoDockerOmitsImagesAndBuildsFrontend(t *testing.T) {
	files, err := Render(config("github", true, true, false, false))
	if err != nil {
		t.Fatal(err)
	}
	for name := range files {
		if strings.HasSuffix(name, "Dockerfile") || strings.HasSuffix(name, ".dockerignore") {
			t.Errorf("unexpected Docker asset %s", name)
		}
	}
	if _, ok := files[".gitlab-ci.yml"]; ok {
		t.Fatal("unexpected GitLab CI")
	}
	workflow := decode(t, files[".github/workflows/frontend-ci.yml"])
	jobs := workflow["jobs"].(map[string]any)
	build := jobs["build"].(map[string]any)
	if !containsRun(build, "npm run build") {
		t.Fatalf("frontend no-Docker build absent: %#v", build)
	}
	if _, ok := files[".github/workflows/python-ci.yml"]; ok {
		t.Fatal("unselected Python workflow rendered")
	}
}

func TestRenderSelectedPythonIndependently(t *testing.T) {
	files, err := Render(config("github", false, false, true, true))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files[".github/workflows/frontend-ci.yml"]; ok {
		t.Fatal("frontend workflow rendered")
	}
	if _, ok := files[".github/workflows/backend-ci.yml"]; ok {
		t.Fatal("backend workflow rendered")
	}
	workflow := decode(t, files[".github/workflows/python-ci.yml"])
	on := workflow["on"].(map[string]any)
	for _, event := range []string{"push", "pull_request"} {
		assertPathList(t, on[event].(map[string]any)["paths"], "services/ai/**", ".github/workflows/python-ci.yml")
	}
	if s := string(files["services/ai/Dockerfile"]); !strings.Contains(s, "python3.12-alpine") || !strings.Contains(s, "UV_PYTHON_DOWNLOADS=never") || !strings.Contains(s, `CMD ["/app/.venv/bin/python", "main.py"]`) {
		t.Fatalf("Python locked runtime contract absent: %s", s)
	}
}

func TestRenderGitLabRulesUseConfiguredPortablePaths(t *testing.T) {
	c := config("gitlab", true, true, false, false)
	c.Frontend.Path = "apps/web"
	c.Backend.Path = "services/api"
	files, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	root := decode(t, files[".gitlab-ci.yml"])
	includes := root["include"].([]any)
	assertChanges(t, includes[0].(map[string]any)["rules"], "apps/web/**")
	assertNoChange(t, includes[0].(map[string]any)["rules"], "frontend/**")
	assertChanges(t, includes[1].(map[string]any)["rules"], "services/api/**")
	if _, ok := files["apps/web/Dockerfile"]; ok {
		t.Fatal("Dockerfile rendered while Docker disabled")
	}
}

func TestRenderWithoutCIStillReturnsRootFiles(t *testing.T) {
	files, err := Render(config("none", true, false, false, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["README.md"]; !ok {
		t.Fatal("README missing")
	}
	for name := range files {
		if strings.HasPrefix(name, ".gitlab") || strings.HasPrefix(name, ".github") {
			t.Fatalf("unexpected CI asset %s", name)
		}
	}
}

func TestRenderRejectsUnsafeComponentPath(t *testing.T) {
	c := config("github", true, false, false, true)
	c.Frontend.Path = "../outside"
	if _, err := Render(c); err == nil {
		t.Fatal("unsafe component path accepted")
	}
}

func TestRenderRejectsInvalidComponentMetadata(t *testing.T) {
	c := config("github", true, false, false, false)
	c.Frontend.Framework = "shell-shaped;value"
	if _, err := Render(c); err == nil {
		t.Fatal("invalid component metadata accepted")
	}
}

func TestGitLabPushRulesDoNotFallThroughForNewBranches(t *testing.T) {
	files, err := Render(config("gitlab", true, false, false, true))
	if err != nil {
		t.Fatal(err)
	}
	root := decode(t, files[".gitlab-ci.yml"])
	if _, ok := root["workflow"].(map[string]any); !ok {
		t.Fatal("workflow rules missing")
	}
	rules := root["include"].([]any)[0].(map[string]any)["rules"].([]any)
	var foundRegular bool
	for _, rule := range rules {
		condition, _ := rule.(map[string]any)["if"].(string)
		if strings.Contains(condition, `$CI_PIPELINE_SOURCE == "push"`) && strings.Contains(condition, "CI_COMMIT_BEFORE_SHA !=") {
			foundRegular = true
			if !strings.Contains(condition, "CI_COMMIT_BEFORE_SHA !=") || !strings.Contains(condition, "CI_COMMIT_TAG == null") {
				t.Fatalf("generic push rule can catch new branches/tags: %s", condition)
			}
		}
	}
	if !foundRegular {
		t.Fatal("regular push rule missing")
	}
}

func TestGitHubImageJobHasScopedPermissionsAndAuthorizedDefaultBranchPush(t *testing.T) {
	files, err := Render(config("github", true, false, false, true))
	if err != nil {
		t.Fatal(err)
	}
	w := decode(t, files[".github/workflows/frontend-ci.yml"])
	if _, ok := w["permissions"].(map[string]any)["packages"]; ok {
		t.Fatal("packages write granted at workflow scope")
	}
	image := w["jobs"].(map[string]any)["image"].(map[string]any)
	if image["permissions"].(map[string]any)["packages"] != "write" {
		t.Fatalf("image permissions = %#v", image["permissions"])
	}
	allSteps := ""
	for _, step := range image["steps"].([]any) {
		b, _ := yaml.Marshal(step)
		allSteps += string(b)
	}
	if !strings.Contains(allSteps, "repository.default_branch") || !strings.Contains(allSteps, "event_name == 'push'") {
		t.Fatalf("image publish condition missing: %s", allSteps)
	}
	if !containsUse(image, "docker/setup-buildx-action@") {
		t.Fatal("setup-buildx action missing")
	}
	if !containsRun(image, "${GITHUB_REPOSITORY,,}") {
		t.Fatal("lowercase GHCR image derivation missing")
	}
}

func TestGitHubBackendTransfersPackagedJarToImageWithoutRecompile(t *testing.T) {
	files, err := Render(config("github", false, true, false, true))
	if err != nil {
		t.Fatal(err)
	}
	w := decode(t, files[".github/workflows/backend-ci.yml"])
	jobs := w["jobs"].(map[string]any)
	pack := jobs["package"].(map[string]any)
	image := jobs["image"].(map[string]any)
	if image["needs"] != "package" {
		t.Fatalf("image needs = %#v", image["needs"])
	}
	packSteps, _ := yaml.Marshal(pack["steps"])
	if !strings.Contains(string(packSteps), "backend/target/*.jar") {
		t.Fatalf("artifact does not use Maven target: %s", packSteps)
	}
	imageSteps, _ := yaml.Marshal(image["steps"])
	if strings.Contains(string(imageSteps), "mvnw") || !strings.Contains(string(imageSteps), "backend/target") {
		t.Fatalf("image must only download packaged jar: %s", imageSteps)
	}
}

func config(ci string, frontend, backend, python, docker bool) *project.Config {
	c := &project.Config{SchemaVersion: 1, Project: "orders", Type: "monorepo", CI: project.CIConfig{Provider: ci}, Container: project.ContainerConfig{Enabled: docker}}
	if frontend {
		c.Frontend = &project.Component{Path: "frontend", Framework: "nextjs", PackageManager: "npm"}
	}
	if backend {
		c.Backend = &project.Component{Path: "backend", Framework: "springboot", BuildTool: "maven", Java: 21}
	}
	if python {
		c.Python = &project.Component{Path: "services/ai", Framework: "python", PackageManager: "uv"}
	}
	return c
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := yaml.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func stringsOf(v any) []string {
	xs := v.([]any)
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x.(string)
	}
	return out
}
func changeText(rules any) string {
	all := ""
	for _, r := range rules.([]any) {
		if c, ok := r.(map[string]any)["changes"]; ok {
			if m, ok := c.(map[string]any); ok {
				c = m["paths"]
			}
			all += strings.Join(stringsOf(c), "\n")
		}
	}
	return all
}
func assertChanges(t *testing.T, rules any, wants ...string) {
	t.Helper()
	all := changeText(rules)
	for _, want := range wants {
		if !strings.Contains(all, want) {
			t.Errorf("changes missing %q in %q", want, all)
		}
	}
}
func assertNoChange(t *testing.T, rules any, unwanted string) {
	t.Helper()
	all := changeText(rules)
	if strings.Contains(all, unwanted) {
		t.Errorf("changes unexpectedly contain %q", unwanted)
	}
}
func hasChangesRule(rules []any) bool {
	for _, r := range rules {
		m := r.(map[string]any)
		if _, ok := m["changes"]; ok && m["when"] != "never" {
			return true
		}
	}
	return false
}
func containsRun(job map[string]any, want string) bool {
	for _, step := range job["steps"].([]any) {
		if run, _ := step.(map[string]any)["run"].(string); strings.Contains(run, want) {
			return true
		}
	}
	return false
}
func containsUse(job map[string]any, want string) bool {
	for _, step := range job["steps"].([]any) {
		if use, _ := step.(map[string]any)["uses"].(string); strings.Contains(use, want) {
			return true
		}
	}
	return false
}
func assertPathList(t *testing.T, v any, wants ...string) {
	t.Helper()
	got := strings.Join(stringsOf(v), "\n")
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("paths missing %q in %q", want, got)
		}
	}
}

func assertGitLabImageGuard(t *testing.T, job map[string]any) {
	t.Helper()
	script := strings.Join(stringsOf(job["script"]), "\n")
	for _, want := range []string{"PUSH=false", `CI_PIPELINE_SOURCE" = "push`, `CI_COMMIT_BRANCH" = "$CI_DEFAULT_BRANCH`, "CI_REGISTRY_PASSWORD", "push=$PUSH"} {
		if !strings.Contains(script, want) {
			t.Errorf("image script missing %q: %s", want, script)
		}
	}
	if !hasChangesRule(job["rules"].([]any)) {
		t.Errorf("image is not path-gated: %#v", job["rules"])
	}
}
