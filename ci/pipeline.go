package main
import (
	"context"
	"log/slog"
	"os"
	"dagger.io/dagger"
)

func main() {
	if err := build(context.Background()); err != nil {
		slog.Error("An error occurred while running pipeline", err)
	}
}

func build(ctx context.Context) error {
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer client.Close()
	src := client.Host().Directory(".")
	builder := client.Container().From("golang:1.22")
	builder = builder.WithDirectory("/src", src).WithWorkdir("/src")
	builder = builder.WithExec([]string{"go", "mod", "download"})
	builder = builder.WithEnvVariable("CGO_ENABLED", "0").WithEnvVariable("GOOS", "linux").WithEnvVariable("GOARCH", "amd64")
	builder = builder.WithExec([]string{"go", "build", "-o", "bin/acch"})
	runtime := client.Container().From("gcr.io/distroless/base-debian12")
	runtime = runtime.WithWorkdir("/app")
	runtime = runtime.WithFile("/app/bin/acch", builder.File("/src/bin/acch"))
	runtime = runtime.WithExposedPort(8080)
	runtime = runtime.WithEntrypoint([]string{"/app/bin/acch"})

	secret := client.SetSecret("password", os.Getenv("CI_REGISTRY_PASSWORD"))
	for _, tag := range []string{"latest", os.Getenv("CI_COMMIT_SHORT_SHA")} {
		urn := fmt.Sprintf("%s:%s", os.Getenv("CI_REGISTRY_IMAGE"), tag)
		image, err := runtime.WithRegistryAuth(os.Getenv("CI_REGISTRY"), os.Getenv("CI_REGISTRY_USER"), secret).Publish(ctx, urn)
		slog.Info("Successfully published", "image", image)
	}
	if err != nil {
		return err
	}
	return nil
}

