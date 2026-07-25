package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProductionDependencies_limitsHTTPHeaderBytes(t *testing.T) {
	// Given
	dependencies := productionDependencies()

	// When
	server := dependencies.newServer(runSettings{}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	// Then
	require.IsType(t, &http.Server{}, server)
	require.Equal(t, 16*1024, server.(*http.Server).MaxHeaderBytes)
}
