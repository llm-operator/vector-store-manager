package embedder

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/schema"
)

func TestAddFile(t *testing.T) {
	const (
		filePath           = "../../testdata/test.txt"
		collectionName0    = "collection0"
		vectorStoreName    = "vector_store_1"
		embeddingModel     = "model1"
		chunkSizeTokens    = 10
		chunkOverlapTokens = 2
		modelName          = "model1"
	)

	tcs := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "success",
			path:    filePath,
			wantErr: false,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			e := New(
				&noopLLMClient{
					e: map[string][]float64{
						"line1": {0.111, 0.122},
						"line2": {0.211, 0.222},
					},
				},
				&noopS3Client{},
				&noopVStoreClient{
					collectionName: collectionName0,
				},
			)
			ctx := context.Background()
			err := e.AddFile(ctx, collectionName0, modelName, tc.path, chunkSizeTokens, chunkOverlapTokens)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestSplitFile(t *testing.T) {
	tcs := []struct {
		name               string
		path               string
		chunkSizeTokens    int64
		chunkOverlapTokens int64
		exp                []schema.Document
		wantErr            bool
	}{
		{
			name:               "text",
			path:               "../../testdata/test.txt",
			chunkSizeTokens:    5,
			chunkOverlapTokens: 2,
			exp: []schema.Document{
				{
					PageContent: "Tokens can be",
				},
				{
					PageContent: "can be thought of as",
				},
			},
			wantErr: false,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			got, err := splitFile(ctx, tc.path, ".txt", tc.chunkSizeTokens, tc.chunkOverlapTokens)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Less(t, 2, len(got))
			assert.Equal(t, tc.exp[0].PageContent, got[0].PageContent)
			assert.Equal(t, tc.exp[1].PageContent, got[1].PageContent)
		})
	}

}