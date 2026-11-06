package fixtures

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
)

func generatedBuild(t *testing.T, config *fakes.GitConfig) {
	t.Helper()
	tool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	tool, err = filepath.Abs(tool)
	if err != nil {
		t.Fatal(err)
	}
	data, err := exec.Command(tool, "env", "-json", "GOMODCACHE", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	var paths map[string]string
	if err := json.Unmarshal(data, &paths); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	config.Framework = filepath.Clean(filepath.Join(filepath.Dir(file), "../../../../../framework"))
	config.RealGo, config.ModuleCache, config.GoCache = tool, paths["GOMODCACHE"], paths["GOCACHE"]
	config.BuildSource = t.TempDir()
	for name, data := range map[string]string{
		"go.mod":   "module example.invalid/workspace-demo-agent\n\ngo 1.26.7\n\nrequire github.com/defos-ai/commander/framework v0.4.1\n",
		"agent.go": generatedAgent, "agent_test.go": generatedAgentTest,
		"cmd/main.go": "package main\nimport generated \"example.invalid/workspace-demo-agent\"\nfunc main() { _ = generated.AgentType }\n",
		"LICENSE":     "Synthetic local build fixture.\n", "AGENTS.md": "Local agent fixture for the Builder serving flow.\n",
		"docker/Dockerfile":                  "FROM scratch\n",
		".github/scripts/release.go":         generatedRelease,
		".github/workflows/make-release.yml": ReleaseWorkflow,
		".github/workflows/test.yml":         "name: Local fixture test\n", ".github/workflows/build-binaries.yml": "name: Local fixture binaries\n",
		".github/workflows/push.yml": "name: Local fixture image\n", ".github/workflows/push-stable.yml": "name: Local fixture stable\n",
	} {
		path := filepath.Join(config.BuildSource, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

const generatedAgent = `package generated
import (
 "context"
 "encoding/json"
 "github.com/defos-ai/commander/framework/agent"
 "github.com/defos-ai/commander/framework/agent/action"
 "github.com/defos-ai/commander/framework/domain/actions"
)
const AgentType agent.AgentType = "workspace-demo-agent"
func inspect(_ context.Context, input map[string]any) (map[string]any, error) {
 return map[string]any{"inspected":len(input)}, nil
}
func NewAgent(config agent.AgentConfig) (*agent.Agent,error) {
 config.Type=AgentType
 executor,err:=action.NewFunc(action.FuncConfig[map[string]any,map[string]any]{
  Action:actions.Action{Name:"inspect",Description:"Inspect supplied input.",InputSchema:json.RawMessage("{\"type\":\"object\"}"),OutputSchema:json.RawMessage("{\"type\":\"object\"}")},Run:inspect,
 })
 if err!=nil { return nil,err }
 config.Actions=[]action.Executor{executor}
 return agent.NewAgent(config)
}
`

const generatedAgentTest = `package generated
import("context";"testing")
func TestInspect(t *testing.T) {
 result,err:=inspect(context.Background(),map[string]any{"finding":"local"})
 if err!=nil || result["inspected"]!=1 {t.Fatalf("inspection result: %v %v",result,err)}
}
`

const generatedRelease = `package main
import (
 "crypto/sha256"
 "encoding/json"
 "flag"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 generated "example.invalid/workspace-demo-agent"
 "github.com/defos-ai/commander/framework/agent/manifest"
)
func main() { if err:=run();err!=nil {fmt.Fprintln(os.Stderr,err);os.Exit(1)} }
func run() error {
 flags:=flag.NewFlagSet("release",flag.ContinueOnError)
 version:=flags.String("version","","");repository:=flags.String("repository","","")
 digest:=flags.String("digest","","");assets:=flags.String("assets","","")
 if err:=flags.Parse(os.Args[2:]);err!=nil{return err}
 kind:=string(generated.AgentType);binary:="commander-"+kind
 image:="ghcr.io/"+strings.Split(*repository,"/")[0]+"/"+binary
 if os.Args[1]=="metadata" {return json.NewEncoder(os.Stdout).Encode(map[string]string{"version":*version,"type":kind,"binary":binary,"image":image})}
 value:=manifest.Manifest{SchemaVersion:1,Version:*version,Type:kind,Responsibilities:[]string{"Inspect local input."},Actions:[]manifest.Action{{Name:"inspect",Description:"Inspect supplied input."}},Native:map[string]manifest.NativeAsset{},Docker:&manifest.Docker{Image:image+"@"+*digest}}
 for _,goos:=range []string{"linux","darwin","windows"} { for _,arch:=range []string{"amd64","arm64"} {
  name:=binary+"_"+goos+"_"+arch;if goos=="windows"{name+=".exe"}
  data,err:=os.ReadFile(filepath.Join(*assets,name));if err!=nil{return err}
  value.Native[goos+"/"+arch]=manifest.NativeAsset{Asset:name,SHA256:fmt.Sprintf("%x",sha256.Sum256(data))}
 }}
 data,err:=manifest.Render(value);if err!=nil{return err}
 return os.WriteFile(filepath.Join(*assets,"agent.yaml"),data,0600)
}
`
