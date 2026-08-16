package main

import (
	"context"
)

// emitAutomationForTool sends NusaShell automation notifications for
// mutating file operations (Watch->Agent loop). Port of the emitAutomationForTool
// helper in files/mcp/server.js.
func emitAutomation(ctx context.Context, toolName string, args map[string]any) {
	emitAutomationForTool(registerServer, ctx, toolName, args)
}

func emitAutomationForTool(_ interface {
	SendNotificationToAllClients(method string, params any)
}, ctx context.Context, toolName string, args map[string]any) {
	type event struct {
		method, etype string
		payload       map[string]any
	}
	var ev *event

	path, _ := args["path"].(string)
	switch toolName {
	case "write":
		ev = &event{"notifications/nusashell/automation", "files.modified", map[string]any{"path": path, "action": "write"}}
	case "patch":
		ev = &event{"notifications/nusashell/automation", "files.modified", map[string]any{"path": path, "action": "patch"}}
	case "append":
		ev = &event{"notifications/nusashell/automation", "files.modified", map[string]any{"path": path, "action": "append"}}
	case "mkdir":
		ev = &event{"notifications/nusashell/automation", "files.modified", map[string]any{"path": path, "action": "mkdir"}}
	case "touch":
		ev = &event{"notifications/nusashell/automation", "files.modified", map[string]any{"path": path, "action": "touch"}}
	case "delete":
		recursive, _ := args["recursive"].(bool)
		ev = &event{"notifications/nusashell/automation", "files.deleted", map[string]any{"path": path, "recursive": recursive}}
	case "move", "copy":
		source, _ := args["source"].(string)
		dest, _ := args["destination"].(string)
		ev = &event{"notifications/nusashell/automation", "files.moved", map[string]any{"source": source, "destination": dest}}
	}
	if ev == nil {
		return
	}
	if registerServer != nil {
		registerServer.SendNotificationToAllClients(ev.method, map[string]any{
			"type":    ev.etype,
			"payload": ev.payload,
		})
	}
}

// registerServer is set in main() so tool handlers can emit automation
// notifications without changing every handler signature.
var registerServer interface {
	SendNotificationToAllClients(method string, params any)
}
