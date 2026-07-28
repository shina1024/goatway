package router

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"slices"

	"goatway/internal/config"
	"goatway/internal/headers"
)

type tokenEntry struct {
	digest [32]byte
	client ClientType
}

type tokenIndex []tokenEntry

func tokenClients(configured map[config.ClientType][]string) tokenIndex {
	var clients tokenIndex
	for clientType, tokens := range configured {
		for _, token := range tokens {
			clients = append(clients, tokenEntry{
				digest: sha256.Sum256([]byte(token)),
				client: ClientType(clientType),
			})
		}
	}
	return clients
}

func (idx tokenIndex) lookup(token string) (ClientType, bool) {
	digest := sha256.Sum256([]byte(token))
	var matched ClientType
	var found int32
	for _, entry := range idx {
		if subtle.ConstantTimeCompare(digest[:], entry.digest[:]) == 1 {
			matched = entry.client
			found = 1
		}
	}
	return matched, found == 1
}

func (route Route) authorize(request *http.Request, tokens tokenIndex) (ClientType, error) {
	if len(route.from.clients) == 0 {
		return "", nil
	}

	token := request.Header.Get(headers.APIToken)
	if token == "" {
		return "", ErrMissingToken
	}
	client, exists := tokens.lookup(token)
	if !exists {
		return "", ErrUnknownToken
	}
	if slices.Contains(route.from.clients, client) {
		return client, nil
	}
	return "", ErrClientNotAllowed
}
