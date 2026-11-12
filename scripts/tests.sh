#!/usr/bin/env bash

set -euo pipefail

# Resolve symbolic links to find the actual script directory
TARGET="${BASH_SOURCE[0]}"
while [ -h "$TARGET" ]; do
  DIR="$( cd -P "$( dirname "$TARGET" )" && pwd )"
  TARGET="$(readlink "$TARGET")"
  [[ $TARGET != /* ]] && TARGET="$DIR/$TARGET"
done
SCRIPT_DIR="$( cd -P "$( dirname "$TARGET" )" && pwd )"

# Ensure the script is run from the project root directory
cd "$SCRIPT_DIR/.."

mkdir -p artifacts

# Detect Docker Compose CLI
if docker compose version >/dev/null 2>&1; then
  DC=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  DC=(docker-compose)
else
  DC=()
fi

FILTER='^(FAIL\t|--- FAIL:|panic:|fatal error:|vet:|ERROR\s)|[Ff]ail(ed)?|[Ee]xception|[Pp]anic|[Ff]atal|Error:|FAILED|✗|\[ERR\]|\[CRIT\]|Connection refused|\b50[023]\b'
EXCLUDE='^=== RUN|^--- PASS|^ok[[:space:]]|^PASS$|^=== PAUSE|^=== CONT|without errors|\[Attempt|expected .* error|SafeFallbackOnDoubleAnalysisFailure|ResponseFailureDoesNotDuplicateRun|UngroundedReplyEscalates|level=INFO'
EXCLUDE_FILE="$SCRIPT_DIR/exclude.txt"

declare -a SUMMARY_LINES=()

print_header() {
  echo ""
  echo "=================================="
  echo "$1"
  echo "=================================="
}

check_docker_access() {
  if [ ${#DC[@]} -eq 0 ]; then
    echo "❌ Docker Compose não encontrado no PATH."
    exit 1
  fi
  if ls /var/run/docker.sock 2>&1 | grep -q "Operation not permitted" || \
     ls ~/.docker/config.json 2>&1 | grep -q "Operation not permitted"; then
    echo ""
    echo "⚠️  SANDBOX LIMITATION DETECTED ⚠️"
    echo "========================================================================="
    echo "macOS is blocking this execution context from accessing your Docker socket."
    echo ""
    echo "To run these tests, please run directly in your macOS Terminal/iTerm:"
    echo ""
    echo "  cd $(pwd) && ./scripts/tests.sh ${1:-all}"
    echo "========================================================================="
    echo ""
    exit 1
  fi
}

summarize_log() {
  local LOG_FILE=$1
  local ERROR_FILE=$2
  local EXIT_CODE=${3:-0}

  if [ "$EXIT_CODE" -eq 0 ]; then
    echo ""
    echo "Summary:"
    echo "- Full log: $LOG_FILE"
    echo "✅ No final failures (any expected test error logs passed)"
    echo ""
    printf "" > "$ERROR_FILE"
    return 0
  fi

  local CLEANED
  CLEANED=$(sed 's/\x1b\[[0-9;]*[A-Za-z]//g' "$LOG_FILE" \
    | grep -Ei "$FILTER" \
    | grep -Ev "$EXCLUDE" || true)

  if [ -f "$EXCLUDE_FILE" ]; then
    local USER_EXCLUDE
    USER_EXCLUDE=$(grep -Ev '^\s*(#|$)' "$EXCLUDE_FILE" | paste -sd '|' -)
    if [ -n "$USER_EXCLUDE" ]; then
      CLEANED=$(echo "$CLEANED" | grep -Ev "$USER_EXCLUDE" || true)
    fi
  fi

  printf '%s\n' "$CLEANED" > "$ERROR_FILE"
  local ERROR_COUNT
  ERROR_COUNT=$(grep -c . "$ERROR_FILE" || true)

  echo ""
  echo "Summary:"
  echo "- Full log: $LOG_FILE"
  echo "- Error log: $ERROR_FILE"
  echo "- Error lines found: $ERROR_COUNT"
  echo ""

  if [ "$ERROR_COUNT" -gt 0 ]; then
    echo "Top errors:"
    echo "----------------------------------"
    head -n 35 "$ERROR_FILE"
    echo "----------------------------------"
    if [ "$ERROR_COUNT" -gt 35 ]; then
      echo "... truncated ($ERROR_COUNT total error lines)"
    fi
  fi
  echo ""
}

parse_go_stats() {
  local CLEAN_LOG=$1
  local PASSED FAILED SKIPPED TOTAL
  PASSED=$(grep -c '^--- PASS:' "$CLEAN_LOG" || true)
  FAILED=$(grep -c '^--- FAIL:' "$CLEAN_LOG" || true)
  SKIPPED=$(grep -c '^--- SKIP:' "$CLEAN_LOG" || true)
  TOTAL=$(( PASSED + FAILED + SKIPPED ))
  echo "✅ Passed: $PASSED  ❌ Failed: $FAILED  ⏭️  Skipped: $SKIPPED  (Total cases: $TOTAL)"
}

run_go_category() {
  local LABEL=$1
  local SLUG=$2
  shift 2

  print_header "$LABEL"

  local LOG_FILE="artifacts/full-${SLUG}.log"
  local ERR_FILE="artifacts/${SLUG}-errors.log"
  local CLEAN_FILE="artifacts/clean-${SLUG}.log"

  local START_TIME=$SECONDS
  local EXIT_CODE=0
  set +e
  "$@" > "$LOG_FILE" 2>&1
  EXIT_CODE=$?
  set -e

  local DURATION=$(( SECONDS - START_TIME ))
  local TIME_STR="${DURATION}s"

  sed 's/\x1b\[[0-9;]*[A-Za-z]//g' "$LOG_FILE" > "$CLEAN_FILE"

  local STATS
  STATS=$(parse_go_stats "$CLEAN_FILE")
  echo "$STATS"
  echo "⏱️  Time spent: $TIME_STR"

  summarize_log "$LOG_FILE" "$ERR_FILE" "$EXIT_CODE"

  if [ "$EXIT_CODE" -ne 0 ]; then
    echo "❌ $LABEL FAILED — printing failure block:"
    echo "========================================================================="
    if grep -q '^--- FAIL:' "$CLEAN_FILE"; then
      grep -B 2 -A 15 '^--- FAIL:\|^FAIL\t' "$CLEAN_FILE" | head -n 80
    else
      tail -n 60 "$CLEAN_FILE"
    fi
    echo "========================================================================="
    SUMMARY_LINES+=("❌ $LABEL ($TIME_STR) — $STATS")
    return "$EXIT_CODE"
  fi

  SUMMARY_LINES+=("✅ $LABEL ($TIME_STR) — $STATS")
  return 0
}

cmd_lint() {
  print_header "LINT & PACK VALIDATE"
  local LOG_FILE="artifacts/full-lint.log"
  local ERR_FILE="artifacts/lint-errors.log"
  local START_TIME=$SECONDS
  local EXIT_CODE=0

  set +e
  {
    echo "▶ go vet ./..."
    go vet ./...
    echo "▶ brain pack validate packs/realestate packs/support"
    go run ./cmd/brain pack validate packs/realestate packs/support
  } > "$LOG_FILE" 2>&1
  EXIT_CODE=$?
  set -e

  local DURATION=$(( SECONDS - START_TIME ))
  summarize_log "$LOG_FILE" "$ERR_FILE" "$EXIT_CODE"

  if [ "$EXIT_CODE" -ne 0 ]; then
    echo "❌ LINT FAILED:"
    echo "========================================================================="
    cat "$LOG_FILE"
    echo "========================================================================="
    SUMMARY_LINES+=("❌ Lint & Pack Validate (${DURATION}s)")
    return "$EXIT_CODE"
  fi

  cat "$LOG_FILE"
  SUMMARY_LINES+=("✅ Lint & Pack Validate (${DURATION}s)")
  return 0
}

cmd_unit() {
  run_go_category "UNIT TESTS (core, config, security, media, pack, llm, channels, store)" "unit" \
    go test -v -race \
      ./internal/core/... \
      ./internal/config \
      ./internal/security/... \
      ./internal/media/... \
      ./internal/pack \
      ./internal/llm/... \
      ./internal/channels/... \
      ./internal/store
}

cmd_brain() {
  run_go_category "BRAIN & GOLDEN FIXTURES (pipeline, gates, grounding, nudge, golden)" "brain" \
    go test -v -race ./internal/brain
}

cmd_api() {
  run_go_category "HTTP API, SSE & MULTI-PACK TESTS (internal/server)" "api" \
    go test -v -race ./internal/server
}

cmd_build() {
  check_docker_access "build"
  print_header "BUILD & DOCKER UP"

  local BUILD_START_TIME
  BUILD_START_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  local START_TIME=$SECONDS
  local EXIT_CODE=0

  set +e
  "${DC[@]}" up -d --build > artifacts/full-build.log 2>&1
  EXIT_CODE=$?
  set -e

  local CRITICAL_ERRORS=""
  if [ "$EXIT_CODE" -eq 0 ]; then
    echo "Aguardando /healthz do Whatshelper Brain (http://127.0.0.1:8080/healthz)..."
    local READY=0
    for _ in $(seq 1 30); do
      if curl -fsS http://127.0.0.1:8080/healthz > /dev/null 2>&1; then
        READY=1
        break
      fi
      sleep 1
    done

    "${DC[@]}" logs --no-color --since "$BUILD_START_TIME" brain connector store-init minio-init > artifacts/startup-docker.log 2>&1 || true

    if [ "$READY" -ne 1 ]; then
      echo "❌ Whatshelper Brain não respondeu em /healthz dentro de 30s."
      EXIT_CODE=1
    fi

    echo "Aguardando /healthz do Connector (http://127.0.0.1:8081/healthz)..."
    local CONN_READY=0
    for _ in $(seq 1 20); do
      if curl -fsS http://127.0.0.1:8081/healthz > /dev/null 2>&1; then
        CONN_READY=1
        break
      fi
      sleep 1
    done
    if [ "$CONN_READY" -ne 1 ]; then
      echo "❌ Whatshelper Connector não respondeu em /healthz dentro de 20s."
      EXIT_CODE=1
    fi

    CRITICAL_ERRORS=$(sed 's/\x1b\[[0-9;]*[A-Za-z]//g' artifacts/startup-docker.log \
      | grep -Ei "level=ERROR|panic:|fatal error:|falha fatal|falha ao inicializar|Connection refused" || true)

    if [ -n "$CRITICAL_ERRORS" ]; then
      echo ""
      echo "❌ CRITICAL CONTAINER ERROR DETECTED DURING STARTUP!"
      echo "========================================================================="
      printf "%s\n" "$CRITICAL_ERRORS" | head -n 30
      echo "========================================================================="
      EXIT_CODE=1
    fi
  fi

  local DURATION=$(( SECONDS - START_TIME ))
  summarize_log artifacts/full-build.log artifacts/build-errors.log "$EXIT_CODE"

  if [ "$EXIT_CODE" -ne 0 ]; then
    if [ -n "$CRITICAL_ERRORS" ]; then
      printf "%s\n" "$CRITICAL_ERRORS" >> artifacts/build-errors.log
    fi
    echo "❌ BUILD/STARTUP FAILED:"
    tail -n 50 artifacts/full-build.log
    SUMMARY_LINES+=("❌ Docker Build & Startup (${DURATION}s)")
    return "$EXIT_CODE"
  fi

  echo "✅ Containers saudáveis e store-init concluído em ${DURATION}s"
  SUMMARY_LINES+=("✅ Docker Build & Startup (${DURATION}s)")
  return 0
}

cmd_integration() {
  check_docker_access "integration"

  # Garante que DynamoDB Local e MinIO estejam rodando antes dos testes de integração
  if ! curl -fsS http://127.0.0.1:8000/ >/dev/null 2>&1 || \
     ! curl -fsS http://127.0.0.1:9000/minio/health/live >/dev/null 2>&1; then
    echo "Subindo dependências locais (dynamodb-local, minio, minio-init, store-init)..."
    "${DC[@]}" up -d dynamodb-local minio minio-init store-init > artifacts/integration-deps.log 2>&1
  fi

  run_go_category "INTEGRATION TESTS (DynamoDB Local & MinIO)" "integration" \
    go test -v -tags=integration ./internal/store/...
}

cmd_live() {
  print_header "LIVE LLM TESTS (.env / Anthropic / Gemini / DeepSeek)"
  if [ -f .env ]; then
    set -a
    # shellcheck disable=SC1091
    . ./.env
    set +a
  fi
  if { [ -z "${ANTHROPIC_API_KEY:-}" ] && [ -z "${GEMINI_API_KEY:-}" ] && [ -z "${DEEPSEEK_API_KEY:-}" ]; } || \
     [ -z "${MODEL_ANALYSIS:-}" ] || [ -z "${MODEL_RESPONSE:-}" ] || [ -z "${MODEL_NUDGE:-}" ]; then
    echo "⚠️  Configure no .env ou ambiente: chave (ANTHROPIC_API_KEY, GEMINI_API_KEY ou DEEPSEEK_API_KEY) + MODEL_ANALYSIS, MODEL_RESPONSE e MODEL_NUDGE."
    return 1
  fi
  run_go_category "LIVE LLM SMOKE & GOLDEN" "live" \
    go test -v -tags=llm_live ./internal/brain/...
}

cmd_e2e() {
  print_header "BROWSER E2E (FUTURO)"
  echo "ℹ️  Suíte E2E de browser (ex.: Playwright contra http://127.0.0.1:8080) ainda não implementada."
  echo "   Os testes E2E atuais de servidor/pipeline já rodam na categoria 'api' (./scripts/tests.sh api)."
  SUMMARY_LINES+=("⏭️  Browser E2E (placeholder futuro — cobertura atual em 'api')")
  return 0
}

cmd_all() {
  local TOTAL_START=$SECONDS
  cmd_lint
  cmd_unit
  cmd_brain
  cmd_api
  cmd_build
  cmd_integration
  cmd_e2e

  local TOTAL_DURATION=$(( SECONDS - TOTAL_START ))
  print_header "RESUMO GERAL (${TOTAL_DURATION}s)"
  for line in "${SUMMARY_LINES[@]}"; do
    echo "$line"
  done
  echo ""
  echo "📄 Logs completos em artifacts/"
}

cmd_logs() {
  if ls artifacts/*.log >/dev/null 2>&1; then
    cat artifacts/*.log
  else
    echo "Nenhum log encontrado em artifacts/."
  fi
}

cmd_docker_errors() {
  check_docker_access "docker-errors"
  local TAIL=${2:-500}
  echo ""
  echo "🐳 DOCKER ERRORS (last $TAIL lines, filtered):"
  echo "========================================================================="
  local CONTAINER_ERRORS
  CONTAINER_ERRORS=$("${DC[@]}" logs --no-color --tail="$TAIL" 2>/dev/null \
    | grep -Ei -A 10 "level=ERROR|panic:|fatal|Connection refused|\b50[023]\b" || true)
  if [ -n "$CONTAINER_ERRORS" ]; then
    printf "%s\n" "$CONTAINER_ERRORS"
  else
    echo "(nenhuma linha de erro encontrada — últimas 30 linhas dos containers:)"
    echo ""
    "${DC[@]}" logs --no-color --tail=30 2>/dev/null
  fi
  echo "========================================================================="
}

cmd_clean() {
  rm -rf artifacts
  echo "✅ Artifacts cleaned"
}

cmd_rm_db() {
  check_docker_access "rm-db"
  print_header "RESET LOCAL DATA (DynamoDB Local & MinIO volumes)"
  "${DC[@]}" down -v || true
  "${DC[@]}" up -d --build
  echo "✅ Infraestrutura local reiniciada com volumes limpos"
}

cmd_prune() {
  check_docker_access "prune"
  echo "🧹 Limpando cache de build e imagens não usadas do Docker..."
  docker builder prune -f
  docker system prune -a --volumes -f
  echo "✅ Docker pruning completed!"
}

usage() {
  echo "Uso: ./scripts/tests.sh [categoria]"
  echo ""
  echo "Categorias disponíveis:"
  echo "  all          Executa tudo (lint + unit + brain + api + build + integration) [padrão]"
  echo "  lint         go vet + brain pack validate"
  echo "  unit         Testes unitários e contratos in-memory (-race)"
  echo "  brain        Pipeline cognitivo, políticas, grounding, nudge e fixtures golden (-race)"
  echo "  api          API HTTP, SSE, auth e E2E multi-pack em memória (-race)"
  echo "  build        docker compose up -d --build + verificação de startup/healthz"
  echo "  integration  Testes de integração contra DynamoDB Local e MinIO"
  echo "  live         Testes opcionais contra API real da Anthropic (-tags=llm_live)"
  echo "  e2e          Placeholder para futuros testes E2E de browser"
  echo ""
  echo "Utilitários:"
  echo "  logs                 Exibe artifacts/*.log"
  echo "  docker-errors [N]    Filtra erros recentes nos containers"
  echo "  rm-db                Derruba containers com -v (limpa DynamoDB/MinIO) e sobe novamente"
  echo "  clean                Remove a pasta artifacts/"
  echo "  prune                Limpa cache/volumes Docker"
  echo "  help                 Mostra esta ajuda"
}

CMD="${1:-all}"
case "$CMD" in
  all)
    cmd_all
    ;;
  lint)
    cmd_lint
    ;;
  unit)
    cmd_unit
    ;;
  brain)
    cmd_brain
    ;;
  api)
    cmd_api
    ;;
  build)
    cmd_build
    ;;
  integration)
    cmd_integration
    ;;
  live)
    cmd_live
    ;;
  e2e)
    cmd_e2e
    ;;
  logs)
    cmd_logs
    ;;
  docker-errors)
    cmd_docker_errors "$@"
    ;;
  clean)
    cmd_clean
    ;;
  rm|rm-db|reset)
    cmd_rm_db
    ;;
  prune)
    cmd_prune
    ;;
  help|-h|--help)
    usage
    ;;
  *)
    usage
    exit 1
    ;;
esac
