.PHONY: build clean

image_name := holt-agent-gatekeeper
demo_dir := demos/production-gatekeeper

build:
	@echo "Building $(image_name)..."
	# Build from project root (.) so we can access cmd/pup, etc.
	docker build -t $(image_name):latest -f $(demo_dir)/Dockerfile .

clean:
	@echo "Removing $(image_name)..."
	docker rmi $(image_name):latest || true
