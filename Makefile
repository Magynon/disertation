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
	aws --endpoint-url=http://localhost:4566 s3 ls

start-localstack:
	@kubectl apply -f k8s/localstack.yaml
	@kubectl wait --for=condition=ready pod -l app=localstack --timeout=30s
	@echo "Starting port-forward in background..."
	@kubectl port-forward svc/localstack 4566:4566 > /dev/null 2>&1 & echo $$! > .localstack-pid
	@sleep 2  # Give port-forward time to bind
	@echo "LocalStack is running on http://localhost:4566"
	@kubectl apply -f k8s/init-localstack.yaml
	@kubectl wait --for=condition=complete job/init-localstack --timeout=60s
	@echo "LocalStack initialization job completed."
	@$(MAKE) list-sqs
	@$(MAKE) list-sns
	@$(MAKE) list-s3

stop-localstack:
	@if [ -f .localstack-pid ]; then \
		kill $$(cat .localstack-pid) && rm .localstack-pid && echo "Stopped port-forward"; \
	else \
		echo "No port-forward process found."; \
	fi
	@kubectl delete job init-localstack
	@kubectl delete -f k8s/localstack.yaml
	@kubectl get pods