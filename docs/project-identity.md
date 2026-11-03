# Kairos 프로젝트 이름과 호환성

2026-11-03 사용자가 프로젝트 이름을 **Kairos(카이로스)**로 확정했습니다. 투자 기회를 포착하고 위험을 관리한다는 제품 방향을 담습니다. 기존 Omni-Folio와 같은 프로젝트이며 별도 앱이나 새 원장을 만들지 않습니다.

GitHub 저장소는 `YouSangSon/Kairos`, 로컬 저장소 폴더는 `Kairos`입니다. README, 앱 표시 이름과 문서 링크는 새 이름을 사용합니다. Mimir는 연결된 정보 수집·분석 프로젝트의 이름을 유지합니다.

## 기존 데이터와 설정

표시 이름과 저장된 데이터의 식별자는 역할이 다릅니다. 이름 변경 때문에 기존 설치를 새 앱으로 인식하거나 원장·인증 설정을 찾지 못하는 일을 막기 위해 다음 호환성 식별자는 유지합니다.

| 식별자 | 유지 이유 |
| --- | --- |
| `data/omni-folio.db`, Compose의 `omni_folio_data` 볼륨 | 기존 원장을 같은 위치에서 계속 사용 |
| `com.omni-folio.kiwoom.mock.readonly.v1` | 기존 macOS 키체인 항목 참조 유지 |
| Android `com.example.omni_folio_client`, iOS `com.example.omniFolioClient` | 설치된 앱의 정체성과 저장 공간 유지 |
| `OMNI_*` 환경 변수, `OmniFolioApp`, `OmniApi` 등 코드 심벌 | 기존 실행 설정과 코드 사용처 유지 |
| Go `omni-folio/services/core`, Dart `omni_folio_client`, Python `omni_research`·`omni-folio-research` | 패키지·모듈 호출 호환성 유지 |
| JSON Schema의 기존 `$id`, 백업·연구 형식과 엔진 식별자 | 기존 산출물의 검증·해시·복구 호환성 유지 |
| 테스트 자원 이름·소유권 표식 | 기존 잔여 자원의 안전한 정리 기준 유지 |

이 식별자를 바꾸려면 표시 이름 변경과 별도로 데이터·설정 마이그레이션을 설계하고 검증해야 합니다. 이번 변경은 주문 권한이나 실거래 활성화 상태를 바꾸지 않습니다.

## 기존 checkout 갱신

실행 중인 개발 서버와 빌드가 종료된 뒤 기존 저장소 폴더를 `Kairos`로 이동합니다. 데이터·키 파일을 새 저장소에 복사하거나 다시 만들 필요가 없습니다. 기존 Git remote의 전송 방식은 유지하고 저장소 이름만 바꿉니다.

```sh
git remote set-url origin https://github.com/YouSangSon/Kairos.git
```

SSH를 사용했다면 기존 host alias와 SSH 설정을 유지한 채 경로만 `YouSangSon/Kairos.git`로 바꿉니다. Flutter의 경로를 포함하는 생성 파일은 새 폴더에서 다시 생성합니다.

```sh
cd apps/client
flutter pub get --offline --enforce-lockfile
```

상세 설계는 [실행 환경과 모노레포](adr/0001-runtime-and-monorepo.md), 현재 기능과 검증 범위는 [README](../README.md)를 따릅니다.
