package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// S3Backend stores workload state in S3 under {Prefix}/{org}/{env}/{workload}/.
type S3Backend struct {
	client *s3.Client
	Bucket string
	Prefix string // optional key prefix, no trailing slash
}

func NewS3Backend(ctx context.Context, bucket, prefix, region string) (*S3Backend, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &S3Backend{
		client: s3.NewFromConfig(cfg),
		Bucket: bucket,
		Prefix: prefix,
	}, nil
}

func (b *S3Backend) key(org, env, workload, filename string) string {
	parts := []string{org, env, workload, filename}
	if b.Prefix != "" {
		parts = append([]string{b.Prefix}, parts...)
	}
	return strings.Join(parts, "/")
}

func (b *S3Backend) getObject(ctx context.Context, key string) ([]byte, string, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, "", nil
		}
		return nil, "", err
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", err
	}
	etag := ""
	if out.ETag != nil {
		etag = strings.Trim(*out.ETag, `"`)
	}
	return data, etag, nil
}

func (b *S3Backend) putObject(ctx context.Context, key string, data []byte, ifMatch string) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(b.Bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	}
	if ifMatch != "" {
		input.IfMatch = aws.String(ifMatch)
	}
	_, err := b.client.PutObject(ctx, input)
	return err
}

func (b *S3Backend) PullState(ctx context.Context, org, env, workload string) ([]byte, string, error) {
	data, etag, err := b.getObject(ctx, b.key(org, env, workload, "state.yaml"))
	if err != nil {
		return nil, "", fmt.Errorf("S3 get state.yaml: %w", err)
	}
	return data, etag, nil
}

func (b *S3Backend) PushState(ctx context.Context, org, env, workload string, stateYAML []byte, etag string) error {
	err := b.putObject(ctx, b.key(org, env, workload, "state.yaml"), stateYAML, etag)
	if err != nil {
		// S3 returns HTTP 412 when If-Match condition fails.
		var respErr *smithyhttp.ResponseError
		if errors.As(err, &respErr) && respErr.HTTPStatusCode() == 412 {
			return &StateConflictError{}
		}
		return fmt.Errorf("S3 put state.yaml: %w", err)
	}
	return nil
}

func (b *S3Backend) PushMeta(ctx context.Context, org, env, workload string, meta *DeployMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return b.putObject(ctx, b.key(org, env, workload, "deploy_meta.json"), data, "")
}

func (b *S3Backend) PushArtifacts(ctx context.Context, org, env, workload string, scoreYAML, manifestsYAML []byte, meta *DeployMeta) error {
	if err := b.putObject(ctx, b.key(org, env, workload, "score.yaml"), scoreYAML, ""); err != nil {
		return fmt.Errorf("S3 put score.yaml: %w", err)
	}
	if err := b.putObject(ctx, b.key(org, env, workload, "manifests.yaml"), manifestsYAML, ""); err != nil {
		return fmt.Errorf("S3 put manifests.yaml: %w", err)
	}
	return b.PushMeta(ctx, org, env, workload, meta)
}

func (b *S3Backend) GetStatus(ctx context.Context, org, env, workload string) (*WorkloadFiles, error) {
	wf := &WorkloadFiles{}

	scoreData, _, err := b.getObject(ctx, b.key(org, env, workload, "score.yaml"))
	if err != nil {
		return nil, fmt.Errorf("S3 get score.yaml: %w", err)
	}
	wf.ScoreYAML = scoreData

	stateData, _, err := b.getObject(ctx, b.key(org, env, workload, "state.yaml"))
	if err != nil {
		return nil, fmt.Errorf("S3 get state.yaml: %w", err)
	}
	wf.StateYAML = stateData

	metaData, _, err := b.getObject(ctx, b.key(org, env, workload, "deploy_meta.json"))
	if err != nil {
		return nil, fmt.Errorf("S3 get deploy_meta.json: %w", err)
	}
	if metaData != nil {
		var meta DeployMeta
		if err := json.Unmarshal(metaData, &meta); err != nil {
			return nil, fmt.Errorf("parse deploy_meta.json: %w", err)
		}
		wf.DeployMeta = &meta
	}
	return wf, nil
}

func (b *S3Backend) GetManifest(ctx context.Context, org, env, workload string) ([]byte, error) {
	data, _, err := b.getObject(ctx, b.key(org, env, workload, "manifests.yaml"))
	if err != nil {
		return nil, fmt.Errorf("S3 get manifests.yaml: %w", err)
	}
	return data, nil
}
