.PHONY: build test clean

build:
	go build -o bin/gbackup .

test:
	go test ./... -v -race

clean:
	rm -rf bin/
