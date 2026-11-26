.PHONY: up down logs models bench train test test-ml test-api test-frontend lint smoke e2e

up:
	docker compose up -d --build --wait
down:
	docker compose down
logs:
	docker compose logs -f --tail=100
models:
	cd ml && uv sync --extra yolo && uv run python scripts/export_onnx.py
bench:
	cd ml && uv run --extra yolo python scripts/benchmark.py --iters 100 --threads 4
train:
	cd ml && uv run --extra yolo python scripts/train_ppe.py --epochs 20
test: test-ml test-api test-frontend
test-ml:
	cd ml && uv run pytest --cov
test-api:
	cd backend && ./scripts/test.sh
test-frontend:
	cd frontend && npm test
lint:
	cd ml && uv run ruff check src tests && uv run mypy
	cd backend && golangci-lint run ./...
	cd frontend && npm run lint && npm run typecheck
smoke:
	./scripts/smoke.sh
e2e:
	cd frontend && E2E_BASE_URL=http://localhost:20900 npx playwright test
