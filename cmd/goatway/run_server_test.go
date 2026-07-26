package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProductionDependencies_limitsHTTPHeaderBytes(t *testing.T) {
	// Given
	dependencies := productionDependencies()

	// When
	server := dependencies.newServer(runSettings{}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	// Then
	require.IsType(t, (*http.Server)(nil), server)
	productionServer := server.(*http.Server)
	require.Equal(t, 16*1024, productionServer.MaxHeaderBytes)
	require.Equal(t, 5*time.Second, productionServer.ReadHeaderTimeout)
}
