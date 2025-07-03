package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type KnativeEventProxy struct {
	functionURL string
	ceClient    cloudevents.Client
}

func NewKnativeEventProxy() *KnativeEventProxy {
	functionURL := os.Getenv("KNATIVE_FUNCTION_URL")
	if functionURL == "" {
		functionURL = "http://localhost:46359" // default for local testing
	}

	// Create CloudEvents HTTP client
	ceClient, err := cloudevents.NewClientHTTP()
	if err != nil {
		log.Fatalf("failed to create CloudEvents client: %v", err)
	}

	return &KnativeEventProxy{
		functionURL: functionURL,
		ceClient:    ceClient,
	}
}

func main() {
	proxy := NewKnativeEventProxy()

	// Create MCP server with tool capabilities
	s := server.NewMCPServer("knative-event-proxy", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, false),
	)

	// Add the main query tool
	s.AddTool(
		mcp.NewTool("query_events",
			mcp.WithDescription("Query the Knative event service for information using LLM processing"),
			mcp.WithString("query", mcp.Required(), mcp.Description("The query string to send to the event service")),
		),
		proxy.handleQueryEvents,
	)

	// Get port from environment or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8002"
	}

	// Start SSE server
	log.Printf("Starting MCP SSE server on port %s", port)
	log.Printf("Proxying to Knative function at: %s", proxy.functionURL)

	sseServer := server.NewSSEServer(s)
	if err := sseServer.Start(fmt.Sprintf("0.0.0.0:%s", port)); err != nil {
		log.Fatal(err)
	}
}

func (kp *KnativeEventProxy) handleQueryEvents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := req.GetString("query", "")
	if query == "" {
		return nil, fmt.Errorf("query parameter is required")
	}

	log.Printf("Processing query: %s", query)

	// Create CloudEvent to send to your Knative Function
	event := cloudevents.NewEvent()
	event.SetID(fmt.Sprintf("mcp-request-%d", time.Now().UnixNano()))
	event.SetType("mcp.query.request")
	event.SetSource("mcp-knative-proxy")
	event.SetSubject("llm-query")

	// Set the data payload that your function expects
	eventData := map[string]interface{}{
		"query": query,
	}
	event.SetData(cloudevents.ApplicationJSON, eventData)

	// Set target URL in context
	ctxWithTarget := cloudevents.ContextWithTarget(ctx, kp.functionURL)

	// Use Request method for request-response pattern
	responseEvent, result := kp.ceClient.Request(ctxWithTarget, event)
	if cloudevents.IsUndelivered(result) {
		return nil, fmt.Errorf("failed to send CloudEvent to Knative function: %v", result)
	}

	if responseEvent == nil {
		return nil, fmt.Errorf("no response event received from Knative function")
	}

	log.Printf("CloudEvent request-response completed successfully")

	// Extract the chat response from the response CloudEvent
	var responseData map[string]interface{}
	if err := responseEvent.DataAs(&responseData); err != nil {
		return nil, fmt.Errorf("failed to extract response data: %w", err)
	}

	chatResponse, ok := responseData["chat"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid response format: missing 'chat' field")
	}

	log.Printf("Query processed successfully, returning response")

	// Return MCP result with the LLM response
	return mcp.NewToolResultText(chatResponse), nil
}
