package eval

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/acp"
)

const ResultSchemaVersion = 2

type Config struct {
	OxBinary    string
	OxRevision  string
	Candidate   string
	TaskPath    string
	OutputDir   string
	Model       string
	Provider    string
	ProviderURL string
	Credential  string
	Repetitions int
	Upstream    http.Handler
	AllowRemote bool
}

type RunIndex struct {
	Schema       int         `json:"schema"`
	TaskID       string      `json:"task_id"`
	TaskRevision string      `json:"task_revision"`
	OxRevision   string      `json:"ox_revision"`
	Candidate    string      `json:"candidate"`
	BinaryDigest string      `json:"binary_digest"`
	PromptDigest string      `json:"prompt_digest"`
	Model        string      `json:"model"`
	Provider     string      `json:"provider"`
	Budget       Budget      `json:"budget"`
	Repetitions  int         `json:"repetitions"`
	Results      []RunResult `json:"results"`
}

type RunResult struct {
	Schema               int      `json:"schema"`
	TaskID               string   `json:"task_id"`
	TaskRevision         string   `json:"task_revision"`
	OxRevision           string   `json:"ox_revision"`
	Candidate            string   `json:"candidate"`
	BinaryDigest         string   `json:"binary_digest"`
	PromptDigest         string   `json:"prompt_digest"`
	Model                string   `json:"model"`
	Provider             string   `json:"provider"`
	Budget               Budget   `json:"budget"`
	Repetitions          int      `json:"repetitions"`
	Repetition           int      `json:"repetition"`
	Success              bool     `json:"success"`
	SuccessCriteria      Success  `json:"success_criteria"`
	Failure              *Failure `json:"failure"`
	LatencyMS            int64    `json:"latency_ms"`
	ProviderAttempts     int      `json:"provider_attempts"`
	ProviderRetries      int      `json:"provider_retries"`
	FailedEditAttempts   int      `json:"failed_edit_attempts"`
	UsageComplete        bool     `json:"usage_complete"`
	TotalTokens          *uint64  `json:"total_tokens"`
	InputTokens          *uint64  `json:"input_tokens"`
	OutputTokens         *uint64  `json:"output_tokens"`
	CostUSD              *float64 `json:"cost_usd"`
	StopReasons          []string `json:"stop_reasons"`
	Permissions          int      `json:"permissions"`
	PermissionRejections int      `json:"permission_rejections"`
	Answer               string   `json:"answer"`
}

type Failure struct {
	Class   string `json:"class"`
	Message string `json:"message"`
}

func Run(ctx context.Context, config Config) (RunIndex, error) {
	if config.OxBinary == "" || config.TaskPath == "" || config.OutputDir == "" || config.Model == "" || config.Candidate == "" {
		return RunIndex{}, errors.New("ox binary, candidate, task, output directory, and model are required")
	}
	if config.Candidate != CandidateExact && config.Candidate != CandidateAnchored {
		return RunIndex{}, fmt.Errorf("candidate must be %q or %q", CandidateExact, CandidateAnchored)
	}
	if config.Repetitions <= 0 {
		return RunIndex{}, errors.New("repetitions must be positive")
	}
	if config.Upstream == nil && !config.AllowRemote && !localProviderURL(config.ProviderURL) {
		return RunIndex{}, errors.New("remote provider endpoint requires explicit live authorization")
	}
	task, err := LoadTask(config.TaskPath)
	if err != nil {
		return RunIndex{}, err
	}
	binaryPath, err := exec.LookPath(config.OxBinary)
	if err != nil {
		return RunIndex{}, fmt.Errorf("resolve Ox binary: %w", err)
	}
	config.OxBinary = binaryPath
	binaryDigest, err := fileDigest(binaryPath)
	if err != nil {
		return RunIndex{}, fmt.Errorf("digest Ox binary: %w", err)
	}
	promptDigest := taskPromptDigest(task)
	if err := os.MkdirAll(config.OutputDir, 0o755); err != nil {
		return RunIndex{}, fmt.Errorf("create evaluation output: %w", err)
	}
	index := RunIndex{
		Schema: ResultSchemaVersion, TaskID: task.ID, TaskRevision: task.revision,
		OxRevision: config.OxRevision, Candidate: config.Candidate, BinaryDigest: binaryDigest,
		PromptDigest: promptDigest, Model: config.Model, Provider: config.Provider,
		Budget: task.Budget, Repetitions: config.Repetitions,
	}
	for repetition := 1; repetition <= config.Repetitions; repetition++ {
		runRoot := filepath.Join(config.OutputDir, fmt.Sprintf("run-%03d", repetition))
		if err := os.Mkdir(runRoot, 0o755); err != nil {
			return index, fmt.Errorf("create fresh repetition directory %s: %w", runRoot, err)
		}
		result := runOnce(ctx, config, task, repetition, binaryDigest, promptDigest)
		index.Results = append(index.Results, result)
		runPath := filepath.Join(runRoot, "result.json")
		if err := writeJSON(runPath, result); err != nil {
			return index, err
		}
	}
	if err := writeJSON(filepath.Join(config.OutputDir, "index.json"), index); err != nil {
		return index, err
	}
	return index, nil
}

func localProviderURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return false
	}
	if strings.EqualFold(parsed.Hostname(), "localhost") {
		return true
	}
	address := net.ParseIP(parsed.Hostname())
	return address != nil && address.IsLoopback()
}

func runOnce(parent context.Context, config Config, task Task, repetition int, binaryDigest, promptDigest string) RunResult {
	started := time.Now()
	result := RunResult{
		Schema: ResultSchemaVersion, TaskID: task.ID, TaskRevision: task.revision,
		OxRevision: config.OxRevision, Candidate: config.Candidate,
		BinaryDigest: binaryDigest, PromptDigest: promptDigest,
		Model: config.Model, Provider: config.Provider,
		Budget: task.Budget, Repetitions: config.Repetitions, Repetition: repetition,
		SuccessCriteria: task.Success, UsageComplete: true,
	}
	runRoot := filepath.Join(config.OutputDir, fmt.Sprintf("run-%03d", repetition))
	workspace := filepath.Join(runRoot, "workspace")
	private := filepath.Join(runRoot, "private")
	if err := os.MkdirAll(private, 0o700); err != nil {
		return failedResult(result, started, "setup", err)
	}
	if err := seedWorkspace(task, workspace); err != nil {
		return failedResult(result, started, "setup", err)
	}
	gateway, err := startGateway(task.Budget.ProviderRequests, config.Upstream, config.ProviderURL)
	if err != nil {
		return failedResult(result, started, "setup", err)
	}
	defer gateway.Close()

	runContext, cancel := context.WithTimeout(parent, time.Duration(task.Budget.TimeoutMS)*time.Millisecond)
	defer cancel()
	credential := config.Credential
	if credential == "" {
		credential = "evaluation-placeholder"
	}
	ordinal := 1
	permissionTotal := 0
	permissionRejectionTotal := 0
	client, err := startProcess(config.OxBinary, workspace, private, config.Model, gateway.BaseURL(), credential, ordinal)
	if err != nil {
		return failedResult(result, started, "setup", err)
	}
	stopped := false
	stopClient := func() error {
		if stopped {
			return nil
		}
		stopped = true
		return client.stop()
	}
	defer stopClient()

	if err := initialize(runContext, client); err != nil {
		return finishFailed(result, started, gateway, private, classifyError(runContext, err), err, stopClient)
	}
	newRaw, _, err := client.call(runContext, "session/new", acp.NewSessionRequest{
		CWD: workspace, MCPServers: []acp.MCPServer{},
	}, "allow", "", 0)
	if err != nil {
		return finishFailed(result, started, gateway, private, classifyError(runContext, err), err, stopClient)
	}
	var newSession acp.NewSessionResponse
	if err := json.Unmarshal(newRaw, &newSession); err != nil || newSession.SessionID == "" {
		return finishFailed(result, started, gateway, private, "protocol", errors.New("invalid session/new response"), stopClient)
	}
	sessionID := newSession.SessionID

	for _, phase := range task.Phases {
		switch phase.Action {
		case "mutate":
			if err := copyOverlay(filepath.Join(task.path, filepath.FromSlash(phase.Overlay)), workspace); err != nil {
				return finishFailed(result, started, gateway, private, "setup", fmt.Errorf("apply fixture mutation: %w", err), stopClient)
			}
		case "restart":
			permissionTotal += client.stats.Permissions
			permissionRejectionTotal += client.stats.PermissionRejections
			if err := stopClient(); err != nil {
				return finishFailed(result, started, gateway, private, "process_exit", err, func() error { return nil })
			}
			ordinal++
			client, err = startProcess(config.OxBinary, workspace, private, config.Model, gateway.BaseURL(), credential, ordinal)
			if err != nil {
				return finishFailed(result, started, gateway, private, "setup", err, func() error { return nil })
			}
			stopped = false
			if err := initialize(runContext, client); err != nil {
				return finishFailed(result, started, gateway, private, classifyError(runContext, err), err, stopClient)
			}
			_, _, err = client.call(runContext, "session/load", acp.LoadSessionRequest{
				SessionID: sessionID, CWD: workspace, MCPServers: []acp.MCPServer{},
			}, "allow", "", 0)
			if err != nil {
				return finishFailed(result, started, gateway, private, classifyError(runContext, err), err, stopClient)
			}
		case "prompt":
			raw, _, callErr := client.call(runContext, "session/prompt", acp.PromptRequest{
				SessionID: sessionID,
				Prompt:    []acp.ContentBlock{{Type: "text", Text: phase.Prompt}},
			}, phase.Permission, sessionID, time.Duration(phase.CancelAfterMS)*time.Millisecond)
			syncResultStats(&result, client, permissionTotal, permissionRejectionTotal)
			if runContext.Err() != nil {
				clearUsage(&result)
				return finishFailed(result, started, gateway, private, "timeout", runContext.Err(), stopClient)
			}
			if callErr != nil {
				clearUsage(&result)
				return finishFailed(result, started, gateway, private, classifyError(runContext, callErr), callErr, stopClient)
			}
			var response acp.PromptResponse
			if err := json.Unmarshal(raw, &response); err != nil {
				clearUsage(&result)
				return finishFailed(result, started, gateway, private, "protocol", err, stopClient)
			}
			result.StopReasons = append(result.StopReasons, string(response.StopReason))
			if response.Usage != nil {
				accumulateUsage(&result, *response.Usage)
			} else {
				clearUsage(&result)
			}
			want := phase.ExpectedStop
			if want == "" {
				want = string(acp.StopReasonEndTurn)
			}
			if string(response.StopReason) != want {
				err := fmt.Errorf("stop reason = %q, want %q", response.StopReason, want)
				return finishFailed(result, started, gateway, private, "protocol", err, stopClient)
			}
		}
	}
	if err := stopClient(); err != nil {
		return finishFailed(result, started, gateway, private, "process_exit", err, func() error { return nil })
	}
	syncResultStats(&result, client, permissionTotal, permissionRejectionTotal)
	output, err := verifyTask(runContext, task, workspace)
	_ = os.WriteFile(filepath.Join(private, "verifier.log"), []byte(output), 0o600)
	if err != nil {
		class := "verifier"
		if runContext.Err() != nil {
			class = "timeout"
		}
		return finishFailed(result, started, gateway, private, class, err, func() error { return nil })
	}
	if result.PermissionRejections < task.Success.MinimumPermissionRejections {
		err := fmt.Errorf("permission rejections = %d, want at least %d", result.PermissionRejections, task.Success.MinimumPermissionRejections)
		return finishFailed(result, started, gateway, private, "verifier", err, func() error { return nil })
	}
	attempts, exceeded := gateway.Counts()
	if exceeded {
		return finishFailed(result, started, gateway, private, "provider_budget", errors.New("provider request budget exceeded"), func() error { return nil })
	}
	logical := logicalProviderRequests(private)
	result.Success = true
	result.LatencyMS = time.Since(started).Milliseconds()
	result.ProviderAttempts = attempts
	result.ProviderRetries = max(0, attempts-logical)
	result.FailedEditAttempts = failedEditAttempts(private)
	return result
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func taskPromptDigest(task Task) string {
	hash := sha256.New()
	for _, phase := range task.Phases {
		if phase.Action == "prompt" {
			_, _ = io.WriteString(hash, phase.Prompt)
			_, _ = hash.Write([]byte{0})
		}
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

func accumulateUsage(result *RunResult, usage acp.Usage) {
	if !result.UsageComplete {
		return
	}
	if result.TotalTokens == nil {
		total, input, output := usage.TotalTokens, usage.InputTokens, usage.OutputTokens
		result.TotalTokens, result.InputTokens, result.OutputTokens = &total, &input, &output
		return
	}
	if usage.TotalTokens >= *result.TotalTokens && usage.InputTokens >= *result.InputTokens && usage.OutputTokens >= *result.OutputTokens {
		*result.TotalTokens, *result.InputTokens, *result.OutputTokens = usage.TotalTokens, usage.InputTokens, usage.OutputTokens
		return
	}
	*result.TotalTokens += usage.TotalTokens
	*result.InputTokens += usage.InputTokens
	*result.OutputTokens += usage.OutputTokens
}

func clearUsage(result *RunResult) {
	result.UsageComplete = false
	result.TotalTokens, result.InputTokens, result.OutputTokens = nil, nil, nil
}

func syncResultStats(result *RunResult, client *processClient, permissionTotal, permissionRejectionTotal int) {
	result.CostUSD = client.stats.CostUSD
	result.Permissions = permissionTotal + client.stats.Permissions
	result.PermissionRejections = permissionRejectionTotal + client.stats.PermissionRejections
	result.Answer = client.stats.Answer.String()
}

func initialize(ctx context.Context, client *processClient) error {
	_, _, err := client.call(ctx, "initialize", acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion,
		ClientInfo:         &acp.Implementation{Name: "ox-eval", Version: "1"},
		ClientCapabilities: &acp.ClientCapabilities{},
	}, "allow", "", 0)
	return err
}

func finishFailed(result RunResult, started time.Time, gateway *providerGateway, private, class string, err error, stop func() error) RunResult {
	if stopErr := stop(); stopErr != nil && class != "process_exit" {
		err = errors.Join(err, stopErr)
	}
	attempts, exceeded := gateway.Counts()
	if exceeded && class != "timeout" {
		class = "provider_budget"
	}
	result.ProviderAttempts = attempts
	result.ProviderRetries = max(0, attempts-logicalProviderRequests(private))
	result.FailedEditAttempts = failedEditAttempts(private)
	return failedResult(result, started, class, err)
}

func failedResult(result RunResult, started time.Time, class string, err error) RunResult {
	if result.TotalTokens == nil {
		result.UsageComplete = false
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	result.Failure = &Failure{Class: class, Message: err.Error()}
	return result
}

func classifyError(ctx context.Context, err error) string {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "invalid json-rpc") {
		return "protocol"
	}
	if strings.Contains(message, "openrouter") || strings.Contains(message, "provider") {
		return "provider"
	}
	if strings.Contains(message, "eof") || strings.Contains(message, "broken pipe") {
		return "process_exit"
	}
	return "protocol"
}

func logicalProviderRequests(private string) int {
	paths, _ := filepath.Glob(filepath.Join(private, "trace-*.jsonl"))
	count := 0
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var record struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Type == "provider_request_started" {
				count++
			}
		}
		_ = file.Close()
	}
	return count
}

func failedEditAttempts(private string) int {
	paths, _ := filepath.Glob(filepath.Join(private, "trace-*.jsonl"))
	count := 0
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var record struct {
				Type     string `json:"type"`
				ToolName string `json:"tool_name"`
				Outcome  string `json:"outcome"`
			}
			if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Type == "tool_completed" &&
				record.ToolName == "edit_file" && record.Outcome == "failed" {
				count++
			}
		}
		_ = file.Close()
	}
	return count
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
