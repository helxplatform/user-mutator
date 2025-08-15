# Makefile

# import config.
# You can change the default config with `make cnf="config_special.env" build`
cnf ?= config.env
include $(cnf)

# Variable for the binary name
BINARY_NAME := user-mutator
# Variable for the container name
BASE_IMAGE := user-mutator
REGISTRY := containers.renci.org/helxplatform
IMAGE_TAG := $(REGISTRY)/$(BASE_IMAGE)
CHART_NAME := $(BASE_IMAGE)
VERSION := $(or $(VERSION),"v1.5.0")

## Kind Related
KIND_CLUSTER := mutator

MAKEFILE_PATH := $(abspath $(lastword $(MAKEFILE_LIST)))
MAKEFILE_DIR := $(dir $(MAKEFILE_PATH))
CERT_DIR := $(MAKEFILE_DIR)certs

# export all variables
export

.PHONY: build go-build go-test push push-version ca-key-cert mutate-config key-cert-secret clean deploy-webhook-server deploy-all kind-up kind-load kind-down kind-all clean-all

# Build the Go application
build:
	docker buildx build \
	--platform=linux/amd64 \
	--tag=$(IMAGE_TAG):$(VERSION) \
	--tag=$(IMAGE_TAG):latest \
	--tag=$(BASE_IMAGE):$(VERSION) \
	--tag=$(BASE_IMAGE):latest \
	.

go-build:
	@echo "Building Go application..."
	cd webhook-server && go build -o $(BINARY_NAME)

# Run tests
go-test:
	@echo "Running tests..."
	cd webhook-server && go test -v ./...

push:
	docker push $(IMAGE_TAG):$(VERSION)
	docker push $(IMAGE_TAG):latest

push-version:
	docker push $(IMAGE_TAG):$(VERSION)

ca-key-cert:
	cd tls-and-mwc && go run main.go createMutationConfig.go generateTLSCerts.go

mutate-config: ca-key-cert
	cd tls-and-mwc && go run main.go createMutationConfig.go generateTLSCerts.go -M
	@echo ""
	@echo "To view the MutationWebhookConfig YAML use the following command."
	@echo "  kubectl get MutatingWebhookConfiguration $(MUTATE_CONFIG) -o yaml"
	@echo ""

enable-mutate-in-namespace:
	kubectl label namespace $(NAMESPACE_TO_MUTATE) enable-$(MUTATE_CONFIG)=true

disable-mutate-in-namespace:
	kubectl label namespace $(NAMESPACE_TO_MUTATE) enable-$(MUTATE_CONFIG)-

key-cert-secret: ca-key-cert
	# create the secret with CA cert and server cert/key
	kubectl create namespace $(WEBHOOK_NAMESPACE) || true && \
	kubectl create secret generic $(SECRET) --from-file=tls.key=$(CERT_DIR)/key.pem --from-file=tls.crt=$(CERT_DIR)/cert.pem --dry-run=client -o yaml | kubectl -n $(WEBHOOK_NAMESPACE) apply -f -
	@echo ""
	@echo "To view the secret YAML use the following command."
	@echo "  kubectl -n $(WEBHOOK_NAMESPACE) get secret $(SECRET) -o yaml"
	@echo ""

clean:
	@echo "Cleaning up..."
	kubectl delete MutatingWebhookConfiguration $(MUTATE_CONFIG) || true && \
	helm -n $(WEBHOOK_NAMESPACE) delete $(CHART_NAME) || true && \
	kubectl -n $(WEBHOOK_NAMESPACE) delete secret $(SECRET) || true && \
	rm -rf ./certs || true && \
	rm -f webhook-server/$(BINARY_NAME)

deploy-webhook-server: key-cert-secret
	helm -n $(WEBHOOK_NAMESPACE) upgrade --install $(CHART_NAME) \
	    --set "image.pullPolicy=IfNotPresent" --set "image.tag=$(VERSION)" \
		--set "config.secrets.cert=$(SECRET)" \
		./chart
	@echo ""
	@echo "To view and follow the logs of the mutator use the following command."
	@echo "  kubectl -n $(WEBHOOK_NAMESPACE) -l app.kubernetes.io/name=user-mutator logs -f"
	@echo ""

deploy-all: deploy-webhook-server mutate-config

kind-up:
	kind create cluster --name $(KIND_CLUSTER)

kind-load: build
	kind load docker-image $(IMAGE_TAG):$(VERSION) --name $(KIND_CLUSTER)
	kind load docker-image $(IMAGE_TAG):latest --name $(KIND_CLUSTER)

kind-down:
	kind delete cluster --name $(KIND_CLUSTER)

kind-all: kind-up build kind-load deploy-webhook-server mutate-config enable-mutate-in-namespace

clean-all: clean kind-down
