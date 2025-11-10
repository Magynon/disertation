# dissertation

first check context: kubectl config get-contexts

kubectl config current-context
kind delete cluster --name localstack-research
kind create cluster --name localstack-research

TODO:
- remove sqs sending from ingestor
- define message from ingestor to ETL
- include all db ops in transaction

- create sqs publisher
