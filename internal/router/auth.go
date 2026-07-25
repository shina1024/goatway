package router

import (
	"net/http"

	"goatway/internal/config"
	"goatway/internal/headers"
)

func tokenClients(configured map[config.ClientType][]string) map[string]ClientType {
	clients := make(map[string]ClientType)
	for clientType, tokens := range configured {
		for _, token := range tokens {
			clients[token] = ClientType(clientType)
		}
	}
	return clients
}

func (route Route) authorize(request *http.Request, tokens map[string]ClientType) (ClientType, error) {
	if len(route.from.clients) == 0 {
		return "", nil
	}

	token := request.Header.Get(headers.APIToken)
	if token == "" {
		return "", ErrMissingToken
	}
	client, exists := tokens[token]
	if !exists {
		return "", ErrUnknownToken
	}
	for _, allowedClient := range route.from.clients {
		if client == allowedClient {
			return client, nil
		}
	}
	return "", ErrClientNotAllowed
}
