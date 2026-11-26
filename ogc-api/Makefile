.PHONY: build run test test-v vet fmt clean

# Build the binary
build:
	go build -o ogc-api.exe .

# Run the server
run:
	go run .

# Run all tests
test:
	go test ./... -count=1

# Run all tests with verbose output
test-v:
	go test ./... -v -count=1

# Static analysis
vet:
	go vet ./...

# Format all Go files
fmt:
	gofmt -w .

# Remove build artifacts
clean:
	del /Q ogc-api.exe 2>nul || true
