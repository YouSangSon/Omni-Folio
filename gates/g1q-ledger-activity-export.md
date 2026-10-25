# G1.21 — 공개 원장 활동 CSV 내보내기

## 문제와 선택

원장 화면의 최근 기록을 밖에서 확인하려면 일관된 관찰의 원본 숫자와 시각이 필요하다. 기존 backup은 복구용 자료이고 계좌·내부 참조까지 포함하므로 열람용 CSV와 역할이 다르다. [기존 Phase A 후속 계획](../docs/omni-folio-plan.md)에 따라 로컬 CLI 한 개를 추가하며 UI·새 저장소·CSV 재가져오기 형식은 만들지 않는다.

`ledger-export -db PATH -out FILE`은 기존 `LedgerActivity` 공개 projection을 재사용한다. HTTP의 50/100행 제한이나 페이지 cursor 계약을 바꾸지 않는다. 내부 조회 한 번의 10,000행 cap을 적용하고 다음 페이지가 있으면 전체 export를 거절한다. 이는 출력 행 수 제한이며 전체 원장 복구 검증의 메모리·처리 시간 상한이 아니다. 별도 SQL·페이지별 관찰 조합보다 기존 일관성·원문 검증을 공유하는 쪽을 선택했다.

## 구현과 계약

- [`ledger_activity.go`](../services/core/ledger_activity.go)의 같은 deferred read transaction에서 schema 이력·현재 replay 정책과 transition proof·원장 snapshot·canonical events를 검증한다. legacy 활성화 전·손상·누락은 CSV 생성으로 우회하지 않는다. 기존 HTTP 응답 shape와 순서는 유지한다.
- [`read_only_db.go`](../services/core/read_only_db.go)는 기존 paper 조회 opener를 중립 이름으로 옮겼다. 인코딩한 file URI·SQLite `mode=ro`·single connection/deferred 설정을 유지한다. `paper-input-status`도 이 helper를 사용한다. 논리적 read-only는 WAL/SHM 보조파일·reader lock 부재를 뜻하지 않는다.
- [`ledger_export.go`](../services/core/ledger_export.go)는 UTF-8·comma·LF, BOM 없는 고정 12열 `type,occurred_at,recorded_at,symbol,quantity,price,fee,currency,amount,counter_currency,counter_amount,is_correction`을 쓴다. optional 값은 빈 셀, correction은 `true/false`이며 최신 occurrence·sequence 순서를 유지한다. decimal과 UTC 소수초는 float 변환 없이 그대로 쓴다. `CASH_VOID`와 원본은 모두 남고, correction 대상과 계좌별 연결은 공개하지 않는다.
- stdout의 `ledger-activity-export.v1` JSON에는 `source=local_ledger`, `broker_freshness=unverified`, revision·마지막 append event의 recorded time·행 수·완성 CSV byte의 SHA256만 담는다. 빈 원장은 header만 있는 CSV와 0행 receipt다. 계좌·event/source/receipt/instrument ID·경로·원본 데이터는 stdout·오류로 내보내지 않는다.
- 출력은 최대 10,000행·32 MiB다. 종목 표기는 Unicode 문자/숫자로 시작하고 문자/숫자·점·밑줄·슬래시·하이픈만 허용한다. 허용하지 않는 문자열은 DB/API 값을 고치거나 접두문자를 붙이지 않고 export 전체를 거절한다. 정상 음수 decimal은 검증된 숫자 원문으로 보존한다.
- 출력 폴더는 실제 경로를 확인하며 현재 사용자 소유이고 group/other write가 없어야 한다. 모든 조상도 현재 사용자 또는 root 소유여야 하며 공유 쓰기는 root 소유 sticky 조상에서만 허용한다. `/tmp` 아래의 private 폴더는 가능하지만 공유 writable 조상 아래의 안전한 leaf만으로는 허용하지 않는다. 기존 폴더의 권한을 변경하지 않는다. 기존 파일·symlink·DB 및 `-wal/-shm/-journal` 경로를 source symlink의 입력 이름과 실제 이름 양쪽에서 거절한다. 같은 폴더의 exclusive `0600` 임시 파일에 CSV를 쓰고 flush/error·sync/close·취소를 확인한 뒤 hardlink로 no-overwrite 공개하고 임시 파일 제거·directory sync를 수행한다. 정리는 이번 임시 파일만 대상으로 한다. publish 이후 sync·stdout 오류가 나면 완성 CSV가 남을 수 있어 final 경로를 무조건 삭제하지 않는다.

[Go CSV Writer](https://pkg.go.dev/encoding/csv#Writer)는 LF 기본값과 flush 뒤 오류 확인을 설명한다. [OWASP CSV Injection](https://community.owasp.org/attacks/CSV_Injection)에 따르면 quoting만으로 수식 해석을 막지 못하며 모든 스프레드시트와 소비자에 통용되는 sanitization도 없다. 따라서 원문을 바꾸는 escape를 새 회계 표현으로 만들지 않고 종목 text 경계를 제한한다. 실제 스프레드시트의 자동 타입 추론·재저장은 decimal·선행 0을 바꿀 수 있으므로 모든 열을 text로 가져온다. 이는 CSV byte의 정확성과 외부 앱 표시 정확성을 구분하는 규칙이다.

기존 `copyFile`은 최종 경로를 먼저 만들어 쓰므로 부분 결과를 공개할 수 있다. 이 경로는 재사용하지 않고 private 임시 파일의 검사가 끝난 뒤 [Go `os.Link`](https://pkg.go.dev/os#Link)로 공개한다. 새 파일 저장 추상화나 별도 manifest는 추가하지 않는다. 실제 동시 writer의 한 winner 검사로 지원 filesystem의 no-overwrite 동작을 확인한다.

## 검증 근거

head `af6e9f7`의 [무료 표준 Core CI RED](https://github.com/YouSangSon/Omni-Folio/actions/runs/40156261837)에서 실제 실행 파일의 `unknown command "ledger-export"`를 확인했다. 전체 Core 검사는 이 실패 뒤 시작하지 않았다. RED fixture의 fee 기대값은 기존 seed의 `-1000`과 맞췄다.

`TestLedgerExport*`는 실제 CLI·12열/receipt hash/0600/redaction, 지원 event 종류·FX 두 leg·원문 정밀도·소수초 순서·정정, empty/10,000/10,001, 실제 유효한 수식형 저장 label·기존 파일/symlink/DB auxiliary 보존, URI escaping·actual read-only 쓰기 거절·취소·schema/legacy/transition 손상, 동시 공개 한 winner와 writer/byte cap 오류를 다룬다. 공유 writable 조상 거절, 실제 부분 임시 CSV가 32 MiB cap에서 실패한 뒤 제거되는 경우와 stdout 실패 뒤 완성 파일 보존도 포함한다. 독립 리뷰에서 빠진 import·조상 경로 검사·실제 부분 쓰기 검사를 찾아 보완했다. 원격 집중/전체 검사와 최종 리뷰 결과는 해당 PR head에서 확인한 뒤 기록한다. 로컬에서는 runtime/DB/native fixture를 실행하지 않으며 format·diff·README/링크의 정적 검사만 수행한다.

## 한계와 재개 조건

이 CSV는 backup·계좌별 export·재가져오기·정정 대상 연결·broker-confirmed 거래·성과/시장 freshness를 제공하지 않는다. 권한·감사·복구 자료는 기존 backup/restore를 사용한다. 출력 후 DB가 바뀔 수 있으며, 같은 사용자에 의한 악의적인 디렉터리 교체·모든 filesystem·실제 정전의 내구성이나 스프레드시트 실행은 검증 범위가 아니다. macOS/Linux의 지원되는 로컬 filesystem과 준비된 출력 폴더를 사용한다. 이번 Goal은 VM을 건너뛴다.
