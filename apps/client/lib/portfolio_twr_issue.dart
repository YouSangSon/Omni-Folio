String portfolioTwrIssueText(String code) => switch (code) {
  'valuation_unavailable' => '현재 자산 평가가 완전하지 않습니다. 저장 가격과 환율을 확인하세요.',
  'missing_historical_fx' => '입출금 경계의 직접 환율이 없습니다.',
  'stale_historical_fx' => '입출금 경계의 환율이 24시간 기준을 넘었습니다.',
  'missing_security_price' => '입출금 경계의 보유 자산 가격이 없습니다.',
  'stale_security_price' => '입출금 경계의 보유 자산 가격이 24시간 기준을 넘었습니다.',
  'ambiguous_security_price_venue' => '보유 자산 가격의 거래소를 하나로 정할 수 없습니다.',
  'split_price_unavailable' => '분할 뒤 수량에 맞는 가격이 없습니다.',
  'ambiguous_split_boundary' => '입출금과 분할의 시각이 같아 가격 단위를 판단할 수 없습니다.',
  'capital_path_unavailable' =>
    '투자 시작 전 잔액이 있거나 중간 자본이 0 이하가 되어 이 계산 기준을 적용할 수 없습니다. 전액 출금을 손실로 표시하지 않습니다.',
  'no_external_cash_flows' => '투자 시작을 정할 외부 입출금 기록이 없습니다.',
  'cash_flow_limit' => '입출금 평가 경계가 현재 계산 한도인 1,000개를 넘었습니다.',
  _ => '현재 정밀도와 계산 범위에서 결과를 확인할 수 없습니다.',
};
