package cli

import (
	"forge/internal/apperror"
	"forge/internal/project"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"io"
	"os"
)

func terminalStreams(in io.Reader, out io.Writer) bool {
	i, ok := in.(*os.File)
	if !ok {
		return false
	}
	o, ok := out.(*os.File)
	return ok && term.IsTerminal(int(i.Fd())) && term.IsTerminal(int(o.Fd()))
}
func (a *app) prompt(cmd *cobra.Command, o *project.Options) error {
	var fields []huh.Field
	if !cmd.Flags().Changed("frontend") {
		fields = append(fields, huh.NewSelect[string]().Title("Frontend").Options(huh.NewOption("Next.js", "nextjs"), huh.NewOption("None", "none")).Value(&o.Frontend))
	}
	if !cmd.Flags().Changed("backend") {
		fields = append(fields, huh.NewSelect[string]().Title("Backend").Options(huh.NewOption("Spring Boot (Java 21)", "springboot"), huh.NewOption("None", "none")).Value(&o.Backend))
	}
	if !cmd.Flags().Changed("python") {
		fields = append(fields, huh.NewSelect[string]().Title("Add Python service?").Options(huh.NewOption("No", "none"), huh.NewOption("Yes, uv", "uv")).Value(&o.Python))
	}
	if !cmd.Flags().Changed("ci") {
		fields = append(fields, huh.NewSelect[string]().Title("CI/CD").Options(huh.NewOption("GitLab", "gitlab"), huh.NewOption("GitHub", "github"), huh.NewOption("None", "none")).Value(&o.CI))
	}
	if !cmd.Flags().Changed("docker") {
		fields = append(fields, huh.NewConfirm().Title("Generate Dockerfiles?").Value(&o.Docker))
	}
	if len(fields) == 0 {
		return nil
	}
	if err := huh.NewForm(huh.NewGroup(fields...)).WithInput(a.in).WithOutput(a.errout).RunWithContext(a.ctx); err != nil {
		return &apperror.Error{Code: "CANCELED", Message: "Project setup canceled.", Cause: err}
	}
	return nil
}
