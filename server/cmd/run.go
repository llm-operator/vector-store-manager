package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/llm-operator/common/pkg/db"
	fv1 "github.com/llm-operator/file-manager/api/v1"
	"github.com/llm-operator/inference-manager/pkg/llmkind"
	"github.com/llm-operator/rbac-manager/pkg/auth"
	v1 "github.com/llm-operator/vector-store-manager/api/v1"
	"github.com/llm-operator/vector-store-manager/server/internal/config"
	"github.com/llm-operator/vector-store-manager/server/internal/embedder"
	"github.com/llm-operator/vector-store-manager/server/internal/milvus"
	"github.com/llm-operator/vector-store-manager/server/internal/ollama"
	"github.com/llm-operator/vector-store-manager/server/internal/s3"
	"github.com/llm-operator/vector-store-manager/server/internal/server"
	"github.com/llm-operator/vector-store-manager/server/internal/store"
	"github.com/llm-operator/vector-store-manager/server/internal/vllm"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const flagConfig = "config"

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "run",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := cmd.Flags().GetString(flagConfig)
		if err != nil {
			return err
		}

		c, err := config.Parse(path)
		if err != nil {
			return err
		}

		if err := c.Validate(); err != nil {
			return err
		}

		if err := run(cmd.Context(), &c); err != nil {
			return err
		}
		return nil
	},
}

func run(ctx context.Context, c *config.Config) error {
	addr := fmt.Sprintf("localhost:%d", c.GRPCPort)
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return err
	}
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			// Do not use the camel case for JSON fields to follow OpenAI API.
			MarshalOptions: protojson.MarshalOptions{
				UseProtoNames:     true,
				EmitDefaultValues: true,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{
				DiscardUnknown: true,
			},
		}),
		runtime.WithIncomingHeaderMatcher(auth.HeaderMatcher),
		runtime.WithHealthzEndpoint(grpc_health_v1.NewHealthClient(conn)),
	)
	if err := v1.RegisterVectorStoreServiceHandlerFromEndpoint(ctx, mux, addr, opts); err != nil {
		return err
	}

	conn, err = grpc.NewClient(c.FileManagerServerAddr, opts...)
	if err != nil {
		return err
	}
	fclient := fv1.NewFilesServiceClient(conn)

	conn, err = grpc.NewClient(c.FileManagerServerInternalAddr, opts...)
	if err != nil {
		return err
	}
	fwClient := fv1.NewFilesInternalServiceClient(conn)

	dbInst, err := db.OpenDB(c.Database)
	if err != nil {
		return err
	}

	st := store.New(dbInst)
	if err := st.AutoMigrate(); err != nil {
		return err
	}

	vstoreClient, err := milvus.New(ctx, c.VectorDatabase)
	if err != nil {
		return err
	}

	var llm embedder.LLMClient
	var dim int
	switch c.LLMEngine {
	case llmkind.Ollama:
		llm = ollama.New(c.LLMEngineAddr)
		dim, err = ollama.Dimension(c.Model)
		if err != nil {
			return err
		}
	case llmkind.VLLM:
		llm = vllm.NewClient(c.LLMEngineAddr)
		dim, err = vllm.Dimension(c.Model)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported llm engine: %s", c.LLMEngine)
	}
	s3Client := s3.NewClient(c.ObjectStore.S3)
	e := embedder.New(llm, s3Client, vstoreClient)

	s := server.New(st, fclient, fwClient, vstoreClient, e, c.Model, dim)

	errCh := make(chan error)
	go func() {
		log.Printf("Starting HTTP server on port %d", c.HTTPPort)
		errCh <- http.ListenAndServe(fmt.Sprintf(":%d", c.HTTPPort), mux)
	}()

	go func() {
		errCh <- s.Run(ctx, c.GRPCPort, c.AuthConfig)
	}()

	go func() {
		s := server.NewInternal(c.Model, e)
		errCh <- s.Run(c.InternalGRPCPort)
	}()

	return <-errCh
}

func init() {
	runCmd.Flags().StringP(flagConfig, "c", "", "Configuration file path")
	_ = runCmd.MarkFlagRequired(flagConfig)
}
