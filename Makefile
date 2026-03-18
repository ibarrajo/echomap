.PHONY: build test proto server client clean

build: proto
	go build -o bin/echomap ./cmd/echomap/
	go build -o bin/echomap-client ./cmd/echomap-client/

test:
	go test ./... -v

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/v1/echomap.proto

server: build
	./bin/echomap

client: build
	./bin/echomap-client $(ARGS)

clean:
	rm -rf bin/
