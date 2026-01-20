package proc

type AgentType string

const (
	AgentAider    AgentType = "aider"
	AgentClaude   AgentType = "claude"
	AgentCline    AgentType = "cline"
	AgentCopilot  AgentType = "copilot"
	AgentCursor   AgentType = "cursor"
	AgentOpenCode AgentType = "opencode"
)

var agentCmdMap = map[string]AgentType{
	"aider":         AgentAider,
	"claude":        AgentClaude,
	"claude-code":   AgentClaude,
	"cline":         AgentCline,
	"copilot-agent": AgentCopilot,
	"cursor":        AgentCursor,
	"opencode":      AgentOpenCode,
}
