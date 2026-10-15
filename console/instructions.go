package console

import (
	"fmt"
	"strings"
)

func connectShell(session Session) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Sandbox %s, session %s\n", session.Name, session.ID)
	if session.Proxy {
		builder.WriteString("# Proxy mode. Keep each provider's production hostname.\n")
		builder.WriteString("# The proxy replaces Authorization on routed hosts. Do not send the service token there.\n")
	} else {
		builder.WriteString("# Direct mode. Send each service token to that service's base URL.\n")
		builder.WriteString("# Append the provider path your client already calls.\n")
	}
	builder.WriteString("\n")
	for _, variable := range session.Variables {
		fmt.Fprintf(&builder, "export %s=%s\n", variable.Name, shellQuote(variable.Value))
	}
	if !session.Proxy {
		builder.WriteString("\n")
		for _, service := range session.Services {
			fmt.Fprintf(&builder, "curl -sS -H \"Authorization: Bearer $%s\" \"$%s\"\n", service.TokenVariable, service.URLVariable)
		}
	}
	return builder.String()
}

func agentPrompt(session Session) string {
	var builder strings.Builder
	builder.WriteString("A Fabricate sandbox is running on this machine. Use it for provider calls in this task.\n")
	builder.WriteString("Do not use production accounts. Do not commit these credentials or paste them into a pull request.\n\n")
	fmt.Fprintf(&builder, "Sandbox: %s\n", session.Name)
	fmt.Fprintf(&builder, "Session: %s\n", session.ID)
	if session.Proxy {
		builder.WriteString("Mode: proxy\n\n")
	} else {
		builder.WriteString("Mode: direct\n\n")
	}
	builder.WriteString("Services:\n")
	for _, service := range session.Services {
		fmt.Fprintf(&builder, "- %s (%s)\n", service.Name, service.DisplayName)
		fmt.Fprintf(&builder, "  Scenario: %s\n", service.Scenario)
		fmt.Fprintf(&builder, "  Base URL: %s\n", service.URL)
		fmt.Fprintf(&builder, "  Bearer token: %s\n", service.Token)
		fmt.Fprintf(&builder, "  Variables: %s, %s\n", service.URLVariable, service.TokenVariable)
		if len(service.ProviderHosts) > 0 {
			fmt.Fprintf(&builder, "  Production hostnames: %s\n", strings.Join(service.ProviderHosts, ", "))
		}
	}
	builder.WriteString("\n")
	if session.Proxy && session.ProxyConnection != nil {
		builder.WriteString("Proxy mode is on. Keep the provider's normal hostname.\n")
		builder.WriteString("The process under test must honor HTTP_PROXY, HTTPS_PROXY, and the CA bundle below.\n")
		builder.WriteString("Do not send the Fabricate bearer token on those hosts. The proxy replaces Authorization.\n")
		fmt.Fprintf(&builder, "Proxy URL: %s\n", session.ProxyConnection.URL)
		fmt.Fprintf(&builder, "CA file: %s\n", session.ProxyConnection.CAPath)
		fmt.Fprintf(&builder, "Routed hosts: %s\n", strings.Join(session.ProxyConnection.Hosts, ", "))
	} else {
		builder.WriteString("Proxy mode is off. Point each client at the service base URL and send Authorization: Bearer <token>.\n")
		builder.WriteString("Production hostnames are not intercepted. Start the sandbox again with proxy if the client must keep those hostnames.\n")
	}
	builder.WriteString("\nThe sandbox binds to loopback, so only a process on this machine can reach it.\n")
	fmt.Fprintf(&builder, "Request log: %s\n", session.RequestLog)
	builder.WriteString("When the task is done, close this sandbox from the Fabricate console (the process started with `fab serve`).\n")
	return builder.String()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
