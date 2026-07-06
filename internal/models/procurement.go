package models

// Procurement workspace slices used by the iag-pm frontend (projectmanangement).
// Kept separate from PM expense Requisitions (int id, finance/procurement Kafka flow).

type ProcurementVendor struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Logo           string   `json:"logo"`
	Category       string   `json:"category"`
	Contact        string   `json:"contact"`
	Email          string   `json:"email"`
	Phone          string   `json:"phone"`
	Address        string   `json:"address"`
	Country        string   `json:"country"`
	TaxID          string   `json:"taxId"`
	Bank           string   `json:"bank"`
	Terms          string   `json:"terms"`
	Rating         float64  `json:"rating"`
	Certifications []string `json:"certifications"`
	Status         string   `json:"status"`
	LeadTime       int      `json:"leadTime"`
	TotalSpend     float64  `json:"totalSpend"`
	OpenPOs        int      `json:"openPOs"`
	OnTimeRate     float64  `json:"onTimeRate"`
}

type ProcurementItem struct {
	ID              string  `json:"id"`
	SKU             string  `json:"sku"`
	Name            string  `json:"name"`
	Category        string  `json:"category"`
	UOM             string  `json:"uom"`
	Stock           int     `json:"stock"`
	Reorder         int     `json:"reorder"`
	LastPrice       float64 `json:"lastPrice"`
	Currency        string  `json:"currency"`
	PreferredVendor string  `json:"preferredVendor"`
}

type ProcurementBudget struct {
	ID        string  `json:"id"`
	Code      string  `json:"code"`
	Period    string  `json:"period"`
	Allocated float64 `json:"allocated"`
	Committed float64 `json:"committed"`
	Spent     float64 `json:"spent"`
	Remaining float64 `json:"remaining"`
	Dept      string  `json:"dept"`
}

type ProcurementLineItem struct {
	ItemID string   `json:"itemId"`
	Qty    int      `json:"qty"`
	Price  *float64 `json:"price,omitempty"`
}

type ProcurementRequisition struct {
	ID        string                `json:"id"`
	Title     string                `json:"title"`
	Dept      string                `json:"dept"`
	Requester string                `json:"requester"`
	Priority  string                `json:"priority"`
	Status    string                `json:"status"`
	CreatedAt string                `json:"createdAt"`
	NeededBy  string                `json:"neededBy"`
	Total     float64               `json:"total"`
	Currency  string                `json:"currency"`
	BudgetID  string                `json:"budgetId"`
	Items     []ProcurementLineItem `json:"items"`
	Notes     string                `json:"notes"`
	LinkedPO  string                `json:"linkedPO,omitempty"`
}

type ProcurementRfqResponse struct {
	Vendor   string  `json:"vendor"`
	Price    float64 `json:"price"`
	LeadTime int     `json:"leadTime"`
	Terms    string  `json:"terms"`
}

type ProcurementRfq struct {
	ID             string                   `json:"id"`
	Title          string                   `json:"title"`
	Items          []ProcurementLineItem    `json:"items"`
	InvitedVendors []string                 `json:"invitedVendors"`
	Status         string                   `json:"status"`
	DueDate        string                   `json:"dueDate"`
	CreatedAt      string                   `json:"createdAt"`
	WinnerVendor   *string                  `json:"winnerVendor"`
	Responses      []ProcurementRfqResponse `json:"responses"`
}

type ProcurementApprover struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Status string `json:"status"`
}

type ProcurementPurchaseOrder struct {
	ID            string                `json:"id"`
	VendorID      string                `json:"vendorId"`
	RequisitionID *string               `json:"requisitionId"`
	Title         string                `json:"title"`
	Items         []ProcurementLineItem `json:"items"`
	Subtotal      float64               `json:"subtotal"`
	Tax           float64               `json:"tax"`
	Shipping      float64               `json:"shipping"`
	Total         float64               `json:"total"`
	Currency      string                `json:"currency"`
	Status        string                `json:"status"`
	CreatedAt     string                `json:"createdAt"`
	ExpectedDate  string                `json:"expectedDate"`
	Terms         string                `json:"terms"`
	BudgetID      string                `json:"budgetId"`
	Approvers     []ProcurementApprover `json:"approvers"`
	PaymentStatus string                `json:"paymentStatus"`
}

type ProcurementGrnLine struct {
	ItemID       string `json:"itemId"`
	OrderedQty   int    `json:"orderedQty"`
	ReceivedQty  int    `json:"receivedQty"`
	Condition    string `json:"condition"`
}

type ProcurementGrn struct {
	ID           string               `json:"id"`
	POID         string               `json:"poId"`
	VendorID     string               `json:"vendorId"`
	ReceivedDate string               `json:"receivedDate"`
	ReceivedBy   string               `json:"receivedBy"`
	Items        []ProcurementGrnLine `json:"items"`
	Notes        string               `json:"notes"`
	Status       string               `json:"status"`
}

type ProcurementInvoice struct {
	ID            string  `json:"id"`
	VendorID      string  `json:"vendorId"`
	POID          *string `json:"poId"`
	GRNID         *string `json:"grnId"`
	InvoiceDate   string  `json:"invoiceDate"`
	DueDate       string  `json:"dueDate"`
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
	Status        string  `json:"status"`
	PaymentDate   string  `json:"paymentDate,omitempty"`
	MatchStatus   string  `json:"matchStatus"`
	PaymentMethod string  `json:"paymentMethod"`
	Exception     string  `json:"exception,omitempty"`
}

type ProcurementContract struct {
	ID          string  `json:"id"`
	VendorID    string  `json:"vendorId"`
	Title       string  `json:"title"`
	StartDate   string  `json:"startDate"`
	EndDate     string  `json:"endDate"`
	Value       float64 `json:"value"`
	Currency    string  `json:"currency"`
	Status      string  `json:"status"`
	RenewalDate string  `json:"renewalDate"`
	Terms       string  `json:"terms"`
}

type ProcurementPayment struct {
	ID          string  `json:"id"`
	InvoiceID   string  `json:"invoiceId"`
	VendorID    string  `json:"vendorId"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Date        string  `json:"date"`
	Method      string  `json:"method"`
	Reference   string  `json:"reference"`
	Status      string  `json:"status"`
	InitiatedBy string  `json:"initiatedBy"`
}

type ProcurementAuditEntry struct {
	ID        int    `json:"id"`
	Timestamp string `json:"timestamp"`
	User      string `json:"user"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
}

type ProcurementProfile struct {
	Name       string `json:"name"`
	Role       string `json:"role"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	Department string `json:"department"`
	Location   string `json:"location"`
}
