PROJECT_DIR := $(shell pwd)

.PHONY: build install generate clean

build:
	cd agent && npm run build
	cd cli && go build -ldflags "-X main.projectDir=$(PROJECT_DIR)" -o ../fly-triage .

install: build
	cp fly-triage /usr/local/bin/fly-triage
	@echo "Installed fly-triage to /usr/local/bin/fly-triage"

generate:
	cd agent && npx tsx src/generate.ts

clean:
	rm -f fly-triage
	rm -rf agent/dist tmp/filtered_*.json
