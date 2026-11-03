# G6 Deployment Promotion Gate

## Pass when

- PostgreSQL migration이 backup, row count, checksum, ledger invariant, order sequence 검증을 통과한다.
- API, worker, runner, execution gateway의 process identity와 최소 secret scope가 분리된다.
- runner lease/fencing, duplicate Job/CronJob, overlapping rollout에서 신규 주문이 fail-closed한다.
- load test가 Kubernetes와 독립 scaling 필요를 증명한다.
- non-root image, read-only filesystem, resource budgets, probes, secret encryption/RBAC가 검증된다.
- 신규 주문 차단 → reconciliation → lease handoff → rollout → smoke → rollback rehearsal가 통과한다.

## Evidence

- Not active. No Kubernetes manifests before this gate entry criteria are met.
- Entry 후 첫 로컬 클러스터 검증은 Kind v0.32+의 Podman provider(`KIND_EXPERIMENTAL_PROVIDER=podman`)를 기준으로 한다. 현재 provision된 Kind cluster는 없다.


<a id="single-node-resource-budget"></a>

## 단일 노드 검증 구성의 자원 예산 (2026-10-25)

문제: 기존 Compose는 non-root·read-only·loopback과 migration 순서를 갖췄지만 실행 CPU·메모리·로그 크기에는 상한이 없었다. 공유 VM에서 한 서비스가 기본값으로 자원을 소비하면 다른 프로젝트와 SSH의 여유를 침범할 수 있다. 컨테이너마다 전체 프로젝트 예산을 반복해서 주는 대신, 같은 예산을 두 서비스로 나누는 방식이다.

선택: `infra/compose.yaml`의 서비스 속성에 `cpus`와 `mem_limit`을 직접 지정한다. migration은 0.25 CPU·512 MiB, core는 0.75 CPU·1536 MiB로 선언 합계가 1 vCPU·2 GiB이다. migration 성공 뒤에만 core가 시작하므로 실행 시점도 겹치지 않는다. 이 배분은 검증을 시작하기 위한 보수적인 설정이며 측정된 최소 요구량이나 처리 성능은 아니다. 새 orchestration 계층이나 daemon 전역 설정 대신 기존 Compose를 재사용했다. [Compose 서비스 설정](https://docs.docker.com/reference/compose-file/services/#cpus), [메모리 상한](https://docs.docker.com/reference/compose-file/services/#mem_limit)을 따른다.

각 서비스의 `json-file` 로그는 `max-size: "10m"`, `max-file: "3"`으로 회전시킨다. [Docker 공식 로그 정책](https://docs.docker.com/engine/logging/drivers/json-file/)의 컨테이너별 옵션이며 Docker daemon을 바꾸거나 재시작하지 않는다. 오래된 회전 로그는 없어질 수 있으므로 필요한 검증 로그는 컨테이너 정리 전에 별도로 보존한다. 원장 named volume과 로그 보존은 서로 다른 수명이다.

가벼운 검증은 `docker compose -f infra/compose.yaml config --format json`의 실제 정규화 결과를 전후 비교한다. 합계 예산과 로그 옵션을 검사하고, 이 세 설정 외 서비스·네트워크·포트·volume·healthcheck·의존 순서가 같아야 한다. 기본 포트와 별도 포트도 loopback 바인딩을 유지해야 한다. 컨테이너 생성·이미지 pull/build·통합 실행 없이 확인할 수 있는 설정 증거다.

실제 Docker Compose 5.5.1 파서의 기본 포트 8080·별도 포트 18080 정규화 검사를 통과했다. CPU 합계 1, 메모리 합계 2,147,483,648 bytes와 두 서비스의 로그 옵션을 확인했고 이 세 설정 외 정규화 결과는 변경 전과 같았다. 네 언어 README의 명령/Mermaid 20개·링크 34개·anchor 7개·heading 30개·inline literal 212개와 로컬 링크도 일치했다. 정규화 memory 값은 숫자 문자열이므로 검사에서만 정수로 읽었다. 근거: `docs/_internal/skill-outputs/goal-20261025-compose-{before,after,custom-port,check}.json`. 실제 컨테이너 실행이나 API·DB 검증 결과는 아니다.

### my-cloud에서 실행하기 전과 후

- 기존 공통 `/home/ubuntu/.cache/personal-project-vm.lock`의 프로세스 잠금을 배포부터 정리까지 보유하고 다른 프로젝트의 점유를 강제로 해제하지 않는다. 현재 서비스·사용자 데이터·volume·백업과 복구 절차를 먼저 확인한다. 잠금·격리 경로·포트·합성 데이터의 작업 소유권이 정해져야 한다.
- 현재 호스트 자원과 새 구성의 전체 예산을 고려한 뒤 `MemAvailable` 1 GiB·디스크 10 GiB 이상을 남긴다. 지속 swap 증가·OOM·디스크 압박이면 새 실행을 보류한다. 이 Compose 상한은 실행 컨테이너용이며 이미지 빌드와 호스트 전체의 사용량을 제한하지 않는다. 기존 linux/amd64 산출물을 우선 재사용하고 VM 빌드가 필요하면 별도 자원 제한을 증명한다.
- 제공사의 이번 청구 기간 트래픽과 초과 과금 차단 가능 여부를 확인한다. 청구 사용량 미확인 또는 무료량 80%인 2.4 TB 이상이면 신규 이미지·대용량 의존성 전송과 공개 데모를 보류한다. 호스트 네트워크 카운터를 청구 사용량으로 대신하지 않는다.
- API는 loopback과 SSH의 비공개 경로로만 확인한다. 공개 서비스에는 별도 인증·TLS가 필요하다. 실행 전 image의 linux/amd64 호환성과 source SHA를 확인하고, 실행 후 `HostConfig.NanoCpus`/`Memory`/`LogConfig`, 실제 listener와 핵심 성공·실패 흐름, 종료 뒤 소유 자원 부재를 확인한다. `compose config` 통과만으로 런타임 상한이 적용됐다고 보고하지 않는다.

이 단일 노드 준비는 위 PostgreSQL·Kubernetes 승격 gate와 별개다. 새 자원 제한의 VM 적용과 실제 API 검증은 아직 완료하지 않았다. SSH 연결과 청구 트래픽을 확인할 수 있을 때 위 조건부터 이어간다.
