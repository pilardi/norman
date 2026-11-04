// Package llm provides provider-specific metadata constants.
package llm

import "github.com/norman/norman/pkg/protocol"

const (
	// ProviderOpenAI identifies the OpenAI-compatible provider.
	ProviderOpenAI = string(protocol.ProviderConfigTypeOpenai)

	// ProviderAnthropic identifies the Anthropic Claude provider.
	ProviderAnthropic = string(protocol.ProviderConfigTypeAnthropic)

	// ProviderOllama identifies the Ollama local-provider.
	ProviderOllama = string(protocol.ProviderConfigTypeOllama)

	// ProviderGemini identifies the Google Gemini provider.
	ProviderGemini = string(protocol.ProviderConfigTypeGemini)

	// ProviderLlamacpp identifies the llama.cpp provider.
	ProviderLlamacpp = string(protocol.ProviderConfigTypeLlamacpp)
)
