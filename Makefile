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

start-dev-stack: start-localstack start-kafka-stack start-psql
	@echo "🚀 Dev stack is fully running: LocalStack + Kafka + Kafka UI + PostgreSQL"

stop-dev-stack: stop-kafka-stack stop-localstack stop-psql
	@echo "🧹 Dev stack fully stopped"

start-services: build-ingestor deploy-ingestor build-parser deploy-parser build-analyzer deploy-analyzer build-gateway deploy-gateway
	@echo "🚀 Services started"

stop-services: delete-ingestor delete-parser delete-analyzer
	@echo "🧹 Services stopped"

# === POSTGRESQL ===

.PHONY: up
start-psql:
	@kubectl --context $(KUBECTL_CONTEXT) apply -f k8s/psql/deployment.yaml
	@kubectl --context $(KUBECTL_CONTEXT) rollout status deployment/postgres
	@kubectl --context $(KUBECTL_CONTEXT) port-forward svc/postgres 5432:5432 > /dev/null 2>&1 & echo $$! > .psql-pid
	@sleep 2
	@echo "✅ PostgreSQL is ready at localhost:5432"

.PHONY: down
stop-psql:
	@if [ -f .psql-pid ]; then \
    		kill $$(cat .psql-pid) && rm .psql-pid && echo "Port-forward stopped."; \
    	else \
    		echo "No port-forward process found."; \
    	fi
	@kubectl --context $(KUBECTL_CONTEXT) delete -f k8s/psql/deployment.yaml --ignore-not-found

# -----------------------
# ✉️ Kafka Producer Job
# -----------------------

send-message: deploy-producer-job delete-producer-job
	@echo "📤 Message sent to Kafka topic 'test-topic' via producer job"

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
# File Parser Deployment
# -----------------------

build-parser:
	docker build -f file-parser/Dockerfile -t file-parser:latest ./file-parser
	kind load docker-image file-parser:latest --name $(KIND_CLUSTER_NAME)

deploy-parser:
	kubectl --context $(KUBECTL_CONTEXT) apply -f k8s/file-parser/deployment.yaml
	kubectl --context $(KUBECTL_CONTEXT) wait --for=condition=available deployment/file-parser --timeout=60s
	kubectl --context $(KUBECTL_CONTEXT) get pods -l app=file-parser

delete-parser:
	kubectl --context $(KUBECTL_CONTEXT) delete deployment file-parser --ignore-not-found=true

# -----------------------
# Trend Analyzer Deployment
# -----------------------

build-analyzer:
	docker build -f trend-analyzer/Dockerfile -t trend-analyzer:latest ./trend-analyzer
	kind load docker-image trend-analyzer:latest --name $(KIND_CLUSTER_NAME)

deploy-analyzer:
	kubectl --context $(KUBECTL_CONTEXT) apply -f k8s/trend-analyzer/deployment.yaml
	kubectl --context $(KUBECTL_CONTEXT) wait --for=condition=available deployment/trend-analyzer --timeout=60s
	kubectl --context $(KUBECTL_CONTEXT) get pods -l app=trend-analyzer

delete-analyzer:
	kubectl --context $(KUBECTL_CONTEXT) delete deployment trend-analyzer --ignore-not-found=true

# -----------------------
# External Gateway Deployment
# -----------------------

build-gateway:
	docker build -f external-gateway/Dockerfile -t external-gateway:latest ./external-gateway
	kind load docker-image external-gateway:latest --name $(KIND_CLUSTER_NAME)

deploy-gateway:
	kubectl --context $(KUBECTL_CONTEXT) apply -f k8s/external-gateway/deployment.yaml
	kubectl --context $(KUBECTL_CONTEXT) wait --for=condition=available deployment/external-gateway --timeout=60s
	kubectl --context $(KUBECTL_CONTEXT) get pods -l app=external-gateway

delete-gateway:
	kubectl --context $(KUBECTL_CONTEXT) delete deployment external-gateway --ignore-not-found=true

# -----------------------
# Helpers for LocalStack and Kafka
# -----------------------
# === AWS Service Lists ===

list-sqs:
	@AWS_ACCESS_KEY_ID=test \
	AWS_SECRET_ACCESS_KEY=test \
	AWS_PAGER="" \
	aws --endpoint-url=http://localhost:4566 --region eu-west-1 sqs list-queues

list-sqs-contents-ingestor-parser-queue:
	@AWS_ACCESS_KEY_ID=test \
	AWS_SECRET_ACCESS_KEY=test \
	AWS_PAGER="" \
	aws --endpoint-url=http://localhost:4566 sqs receive-message \
        --queue-url http://localhost:4566/000000000000/ingestor-parser-queue \
        --region eu-west-1

list-sqs-contents-parser-analyzer-queue:
	@AWS_ACCESS_KEY_ID=test \
	AWS_SECRET_ACCESS_KEY=test \
	AWS_PAGER="" \
	aws --endpoint-url=http://localhost:4566 sqs receive-message \
        --queue-url http://localhost:4566/000000000000/parser-analyzer-queue \
        --region eu-west-1

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

start-kafka-stack: start-kafka start-kafka-init
	@echo "✅ Kafka stack is running (Kafka + Kafka UI + Init Job)"

stop-kafka-stack: stop-kafka stop-kafka-init
	@echo "🛑 Kafka stack stopped"