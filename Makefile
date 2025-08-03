.PHONY: \
	list-sqs list-sns list-s3 \
	start-localstack stop-localstack \
	start-kafka stop-kafka \
	start-kafka-ui stop-kafka-ui \
	start-kafka-stack stop-kafka-stack \
	start-dev-stack stop-dev-stack

KIND_CLUSTER_NAME := localstack-research
KUBECTL_CONTEXT := kind-dev

# === DEVELOPMENT STACK ===

start-project: start-dev-stack start-services
	@echo "🚀 Project is fully running"

stop-project: stop-services stop-dev-stack
	@echo "🧹 Project fully stopped"

start-dev-stack: start-localstack start-kafka-stack
	@echo "🚀 Dev stack is fully running: LocalStack + Kafka + Kafka UI"

stop-dev-stack: stop-kafka-stack stop-localstack
	@echo "🧹 Dev stack fully stopped"

start-services: build-ingestor deploy-ingestor
	@echo "🚀 Services started: Data Ingestor deployed"

stop-services: delete-ingestor
	@echo "🧹 Services stopped: Data Ingestor deleted"

# -----------------------
# ✉️ Kafka Producer Job
# -----------------------

build-producer:
	docker build -f data-ingestor/Dockerfile.producer -t kafka-producer:latest ./data-ingestor
	kind load docker-image kafka-producer:latest --name $(KIND_CLUSTER_NAME)

deploy-producer-job:
	kubectl --context $(KUBECTL_CONTEXT) apply -f k8s/kafka/job-kafka-producer.yaml
	kubectl --context $(KUBECTL_CONTEXT) wait --for=condition=complete job/kafka-producer --timeout=60s
	kubectl --context $(KUBECTL_CONTEXT) logs job/kafka-producer

delete-producer-job:
	kubectl --context $(KUBECTL_CONTEXT) delete job kafka-producer --ignore-not-found=true

# -----------------------
# 📥 Data Ingestor Deployment
# -----------------------

build-ingestor:
	docker build -f data-ingestor/Dockerfile.ingestor -t data-ingestor:latest ./data-ingestor
	kind load docker-image data-ingestor:latest --name $(KIND_CLUSTER_NAME)

deploy-ingestor:
	kubectl --context $(KUBECTL_CONTEXT) apply -f k8s/data-ingestor/deployment.yaml
	kubectl --context $(KUBECTL_CONTEXT) wait --for=condition=available deployment/data-ingestor --timeout=60s
	kubectl --context $(KUBECTL_CONTEXT) get pods -l app=data-ingestor

delete-ingestor:
	kubectl --context $(KUBECTL_CONTEXT) delete deployment data-ingestor --ignore-not-found=true

# -----------------------
# Helpers for LocalStack and Kafka
# -----------------------
# === AWS Service Lists ===

list-sqs:
	@AWS_ACCESS_KEY_ID=test \
	AWS_SECRET_ACCESS_KEY=test \
	AWS_PAGER="" \
	aws --endpoint-url=http://localhost:4566 --region eu-west-1 sqs list-queues

list-sns:
	@AWS_ACCESS_KEY_ID=test \
	AWS_SECRET_ACCESS_KEY=test \
	AWS_PAGER="" \
	aws --endpoint-url=http://localhost:4566 --region eu-west-1 sns list-topics

list-s3:
	@AWS_ACCESS_KEY_ID=test \
	AWS_SECRET_ACCESS_KEY=test \
	AWS_PAGER="" \
	aws --endpoint-url=http://localhost:4566 s3 ls;

list-s3-contents:
	@AWS_ACCESS_KEY_ID=test \
	AWS_SECRET_ACCESS_KEY=test \
	AWS_PAGER="" \
	AWS_S3_USE_PATH_STYLE=1 \
	aws --endpoint-url=http://localhost:4566 s3 ls s3://my-local-bucket --recursive

# === LocalStack ===

start-localstack:
	@echo "🔌 Starting LocalStack..."
	@kubectl config use-context kind-dev
	@kubectl apply -f k8s/localstack.yaml
	@kubectl wait --for=condition=ready pod -l app=localstack --timeout=60s
	@echo "🌐 Port-forwarding LocalStack..."
	@kubectl port-forward svc/localstack 4566:4566 > /dev/null 2>&1 & echo $$! > .localstack-pid
	@sleep 2
	@echo "🚀 Running LocalStack init job..."
	@kubectl apply -f k8s/init-localstack.yaml
	@kubectl wait --for=condition=complete job/init-localstack --timeout=60s
	@echo "✅ LocalStack is ready at http://localhost:4566"
	@$(MAKE) list-sqs
	@$(MAKE) list-sns
	@$(MAKE) list-s3

stop-localstack:
	@echo "🛑 Stopping LocalStack..."
	@if [ -f .localstack-pid ]; then \
		kill $$(cat .localstack-pid) && rm .localstack-pid && echo "Port-forward stopped."; \
	else \
		echo "No port-forward process found."; \
	fi
	@kubectl delete job init-localstack --ignore-not-found=true
	@kubectl delete -f k8s/localstack.yaml --ignore-not-found=true
	@kubectl get pods

# === Kafka ===

start-kafka:
	@echo "📦 Starting Kafka + Zookeeper..."
	@kubectl apply -f k8s/kafka/kafka.yaml
	@kubectl wait --for=condition=ready pod -l app=zookeeper --timeout=60s
	@kubectl wait --for=condition=ready pod -l app=kafka --timeout=60s
	@echo "✅ Kafka is running in-cluster."
	@echo "🌐 Port-forwarding Kafka..."
	@kubectl port-forward svc/kafka 9092:9092 > /dev/null 2>&1 & echo $$! > .kafka-pid
	@sleep 2
	@echo "✅ Kafka available at localhost:9092"

stop-kafka:
	@echo "🛑 Stopping Kafka + Zookeeper..."
	@if [ -f .kafka-pid ]; then \
		PID=$$(cat .kafka-pid); \
		kill $$PID && rm .kafka-pid; \
		while kill -0 $$PID 2>/dev/null; do \
			echo "Waiting for Kafka port-forward to stop..."; \
			sleep 1; \
		done; \
		echo "Port-forward stopped."; \
	else \
		echo "No Kafka port-forward process found."; \
	fi
	@kubectl delete -f k8s/kafka/kafka.yaml --ignore-not-found=true
	@kubectl get pods

start-kafka-init:
	@echo "Running Kafka init job..."
	@kubectl apply -f k8s/kafka/init-kafka.yaml
	@kubectl wait --for=condition=complete job/init-kafka --timeout=60s
	@echo "✅ Kafka init job completed."

stop-kafka-init:
	@kubectl delete job init-kafka --ignore-not-found=true

# === Kafka UI ===

start-kafka-ui:
	@echo "📺 Starting Kafka UI..."
	@kubectl apply -f k8s/kafka/kafka-ui.yaml
	@kubectl wait --for=condition=ready pod -l app=kafka-ui --timeout=60s
	@echo "🌐 Port-forwarding Kafka UI..."
	@kubectl port-forward svc/kafka-ui 8080:8080 > /dev/null 2>&1 & echo $$! > .kafka-ui-pid
	@sleep 2
	@echo "✅ Kafka UI available at http://localhost:8080"

stop-kafka-ui:
	@echo "🛑 Stopping Kafka UI..."
	@if [ -f .kafka-ui-pid ]; then \
		PID=$$(cat .kafka-ui-pid); \
		kill $$PID && rm .kafka-ui-pid; \
		while kill -0 $$PID 2>/dev/null; do \
			echo "Waiting for Kafka UI port-forward to stop..."; \
			sleep 1; \
		done; \
		echo "Port-forward stopped."; \
	else \
		echo "No Kafka UI port-forward process found."; \
	fi
	@kubectl delete -f k8s/kafka/kafka-ui.yaml --ignore-not-found=true
	@kubectl get pods

# === Kafka Stack (Kafka + UI) ===

start-kafka-stack: start-kafka start-kafka-ui start-kafka-init
	@echo "✅ Kafka stack is running (Kafka + Kafka UI + Init Job)"

stop-kafka-stack: stop-kafka-ui stop-kafka stop-kafka-init
	@echo "🛑 Kafka stack stopped"