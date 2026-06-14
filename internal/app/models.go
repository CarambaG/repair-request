package app

import "time"

type Role string

const (
	RoleCustomer   Role = "customer"
	RoleAccountant Role = "accountant"
	RoleWorker     Role = "worker"
)

type Status string

const (
	StatusSentToAccountant     Status = "sent_to_accountant"
	StatusSentToCustomer       Status = "sent_to_customer"
	StatusReturnedToAccountant Status = "returned_to_accountant"
	StatusApprovedForWorkers   Status = "approved_for_workers"
	StatusInProgress           Status = "in_progress"
	StatusCompleted            Status = "completed"
	StatusCancelled            Status = "cancelled"
)

type User struct {
	ID    int64
	Email string
	Name  string
	Role  Role
}

type Session struct {
	ID        int64
	UserID    int64
	CSRFToken string
	ExpiresAt time.Time
}

type RepairRequest struct {
	ID                  int64
	Title               string
	Description         string
	Address             string
	CustomerID          int64
	CustomerName        string
	CustomerEmail       string
	Status              Status
	EstimateAmountCents *int64
	AccountantComment   string
	CustomerComment     string
	WorkerComment       string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type ServiceOption struct {
	ID   int64
	Code string
	Name string
}

type RequestServiceItem struct {
	ID          int64
	RequestID   int64
	ServiceCode string
	ServiceName string
	Quantity    int
}

type RequestForm struct {
	Title             string
	Description       string
	Address           string
	ServiceQuantities map[string]string
}

type RequestAttachment struct {
	ID             int64
	RequestID      int64
	UploadedBy     int64
	UploadedByName string
	OriginalName   string
	StoredName     string
	ContentType    string
	SizeBytes      int64
	CreatedAt      time.Time
	DeletedAt      *time.Time
	DeletedBy      *int64
}

type RequestEvent struct {
	ID         int64
	RequestID  int64
	ActorName  string
	ActorRole  Role
	FromStatus Status
	ToStatus   Status
	Comment    string
	CreatedAt  time.Time
}

func roleLabel(role Role) string {
	switch role {
	case RoleCustomer:
		return "заказчик"
	case RoleAccountant:
		return "бухгалтер"
	case RoleWorker:
		return "рабочий"
	default:
		return string(role)
	}
}

func statusLabel(status Status) string {
	switch status {
	case "created":
		return "создана"
	case StatusSentToAccountant:
		return "передана бухгалтеру"
	case StatusSentToCustomer:
		return "передана заказчику"
	case StatusReturnedToAccountant:
		return "возвращена бухгалтеру"
	case StatusApprovedForWorkers:
		return "передана рабочим"
	case StatusInProgress:
		return "в работе"
	case StatusCompleted:
		return "завершена"
	case StatusCancelled:
		return "отменена"
	default:
		return string(status)
	}
}
