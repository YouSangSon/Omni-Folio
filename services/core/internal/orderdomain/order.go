package orderdomain

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

var decimalPattern = regexp.MustCompile(`^(?:0|-?(?:[1-9][0-9]*(?:\.[0-9]*[1-9])?|0\.[0-9]*[1-9]))$`)

type Event struct {
	EventID                   string `json:"event_id"`
	OrderID                   string `json:"order_id"`
	Type                      string `json:"type"`
	Source                    string `json:"source"`
	ProviderOrderRef          string `json:"provider_order_ref,omitempty"`
	ProviderExecutionRef      string `json:"provider_execution_ref,omitempty"`
	Quantity                  string `json:"quantity,omitempty"`
	Price                     string `json:"price,omitempty"`
	OccurredAt                string `json:"occurred_at,omitempty"`
	RiskReservationID         string `json:"risk_reservation_id,omitempty"`
	PaperAuthorizationID      string `json:"paper_authorization_id,omitempty"`
	RiskPolicyVersion         string `json:"risk_policy_version,omitempty"`
	FencingToken              int64  `json:"fencing_token,omitempty"`
	PaperAccountingSessionID  string `json:"paper_accounting_session_id,omitempty"`
	PaperSignalEventID        string `json:"paper_signal_event_id,omitempty"`
	PaperBarObservationID     string `json:"paper_bar_observation_id,omitempty"`
	PaperFillPolicyVersion    string `json:"paper_fill_policy_version,omitempty"`
	ExecutionAuthorityEventID string `json:"execution_authority_event_id,omitempty"`
	ReferencePrice            string `json:"reference_price,omitempty"`
	Fee                       string `json:"fee,omitempty"`
	Tax                       string `json:"tax,omitempty"`
	Slippage                  string `json:"slippage,omitempty"`
}

type State struct {
	OrderID           string   `json:"order_id"`
	ClientOrderID     string   `json:"client_order_id"`
	AccountRef        string   `json:"account_ref"`
	Status            string   `json:"status"`
	Quantity          string   `json:"quantity"`
	LimitPrice        string   `json:"limit_price"`
	FilledQuantity    string   `json:"filled_quantity"`
	ProviderOrderRefs []string `json:"provider_order_refs"`
	PendingAction     string   `json:"pending_action,omitempty"`
}

func NewState(orderID, clientOrderID, accountRef, quantity, limitPrice string) *State {
	return &State{
		OrderID: orderID, ClientOrderID: clientOrderID, AccountRef: accountRef,
		Quantity: quantity, LimitPrice: limitPrice, FilledQuantity: "0", ProviderOrderRefs: []string{},
	}
}

func Transition(current *State, event Event) (*State, error) {
	if current == nil {
		return nil, errors.New("order state is required")
	}
	next := *current
	next.ProviderOrderRefs = append([]string(nil), current.ProviderOrderRefs...)
	switch event.Type {
	case "INTENT_RECORDED":
		if next.Status != "" {
			return nil, errors.New("intent was already recorded")
		}
		next.Status = "RECORDED"
	case "RISK_APPROVED":
		if next.Status != "RECORDED" {
			return nil, errors.New("risk approval requires a recorded order")
		}
		next.Status = "READY"
	case "RISK_REJECTED":
		if next.Status != "RECORDED" {
			return nil, errors.New("risk rejection requires a recorded order")
		}
		next.Status = "RISK_REJECTED"
	case "SUBMIT_DISPATCHED":
		if next.Status != "READY" || next.PendingAction != "" {
			return nil, errors.New("submit dispatch requires a ready order")
		}
		next.Status, next.PendingAction = "SUBMIT_UNKNOWN", "SUBMIT"
	case "SUBMIT_ACKNOWLEDGED":
		if next.Status != "SUBMIT_UNKNOWN" || next.PendingAction != "SUBMIT" {
			return nil, errors.New("submit acknowledgement requires an unknown submit")
		}
		if err := bindProviderOrderRef(&next, event.ProviderOrderRef); err != nil {
			return nil, err
		}
		next.Status, next.PendingAction = "OPEN", ""
	case "SUBMIT_REJECTED":
		if next.Status != "SUBMIT_UNKNOWN" || next.PendingAction != "SUBMIT" {
			return nil, errors.New("submit rejection requires an unknown submit")
		}
		next.Status, next.PendingAction = "REJECTED", ""
	case "FILL_RECORDED":
		if next.Status != "SUBMIT_UNKNOWN" && next.Status != "OPEN" && next.Status != "PARTIALLY_FILLED" &&
			next.Status != "CANCEL_UNKNOWN" && next.Status != "CANCELED" {
			return nil, errors.New("fill is not valid for the current order state")
		}
		total, err := parseDecimal(next.Quantity)
		if err != nil || total.Sign() <= 0 {
			return nil, errors.New("order quantity is invalid")
		}
		filled, err := parseDecimal(next.FilledQuantity)
		if err != nil || filled.Sign() < 0 {
			return nil, errors.New("filled quantity is invalid")
		}
		quantity, err := parseDecimal(event.Quantity)
		if err != nil || quantity.Sign() <= 0 {
			return nil, errors.New("fill quantity is invalid")
		}
		if err := bindProviderOrderRef(&next, event.ProviderOrderRef); err != nil {
			return nil, err
		}
		filled.Add(filled, quantity)
		if filled.Cmp(total) > 0 {
			return nil, errors.New("fill exceeds order quantity")
		}
		formatted, err := formatDecimal(filled)
		if err != nil {
			return nil, err
		}
		next.FilledQuantity = formatted
		priorStatus := next.Status
		if next.PendingAction == "SUBMIT" {
			next.PendingAction = ""
		}
		switch {
		case filled.Cmp(total) == 0:
			next.Status = "FILLED"
		case priorStatus == "CANCEL_UNKNOWN":
			next.Status = "CANCEL_UNKNOWN"
		case priorStatus == "CANCELED":
			next.Status = "CANCELED"
		default:
			next.Status = "PARTIALLY_FILLED"
		}
	case "CANCEL_DISPATCHED":
		if (next.Status != "OPEN" && next.Status != "PARTIALLY_FILLED") || next.PendingAction != "" {
			return nil, errors.New("cancel dispatch requires an open order")
		}
		next.Status, next.PendingAction = "CANCEL_UNKNOWN", "CANCEL"
	case "CANCEL_ACKNOWLEDGED":
		if next.PendingAction != "CANCEL" || (next.Status != "CANCEL_UNKNOWN" && next.Status != "FILLED") {
			return nil, errors.New("cancel acknowledgement requires an unknown cancel")
		}
		next.PendingAction = ""
		if next.FilledQuantity == next.Quantity {
			next.Status = "FILLED"
		} else {
			next.Status = "CANCELED"
		}
	case "CANCEL_REJECTED":
		if next.PendingAction != "CANCEL" || (next.Status != "CANCEL_UNKNOWN" && next.Status != "FILLED") {
			return nil, errors.New("cancel rejection requires an unknown cancel")
		}
		next.PendingAction = ""
		if next.FilledQuantity == next.Quantity {
			next.Status = "FILLED"
		} else if next.FilledQuantity == "0" {
			next.Status = "OPEN"
		} else {
			next.Status = "PARTIALLY_FILLED"
		}
	default:
		return nil, fmt.Errorf("unsupported order event %q", event.Type)
	}
	return &next, nil
}

func SameProviderExecution(left, right Event) bool {
	left.EventID, right.EventID = "", ""
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func bindProviderOrderRef(state *State, ref string) error {
	if len(state.ProviderOrderRefs) == 0 {
		state.ProviderOrderRefs = append(state.ProviderOrderRefs, ref)
		return nil
	}
	if state.ProviderOrderRefs[0] != ref {
		return errors.New("provider order reference changed")
	}
	return nil
}

func parseDecimal(raw string) (*big.Rat, error) {
	if !decimalPattern.MatchString(raw) {
		return nil, errors.New("non-canonical decimal")
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok {
		return nil, errors.New("invalid decimal")
	}
	return value, nil
}

func formatDecimal(value *big.Rat) (string, error) {
	if value.Sign() == 0 {
		return "0", nil
	}
	denominator := new(big.Int).Set(value.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	twos, fives := 0, 0
	zero := new(big.Int)
	for new(big.Int).Mod(denominator, two).Cmp(zero) == 0 {
		denominator.Div(denominator, two)
		twos++
	}
	for new(big.Int).Mod(denominator, five).Cmp(zero) == 0 {
		denominator.Div(denominator, five)
		fives++
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", fmt.Errorf("exact value %s has no finite decimal representation", value.RatString())
	}
	formatted := value.FloatString(max(twos, fives))
	if strings.Contains(formatted, ".") {
		formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	}
	return formatted, nil
}
