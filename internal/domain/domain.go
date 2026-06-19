package domain

import "time"

type Status string

const (
	StatusNew       Status = "new"
	StatusPlanned   Status = "planned"
	StatusDone      Status = "done"
	StatusPostponed Status = "postponed"
	StatusCancelled Status = "cancelled"
)

type Priority string

const (
	PriorityP1 Priority = "p1"
	PriorityP2 Priority = "p2"
	PriorityP3 Priority = "p3"
	PriorityP4 Priority = "p4"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleGuest  Role = "guest"
)

type RecurrenceRule string

const (
	RecurrenceDaily RecurrenceRule = "daily"
)

type User struct {
	ID         int64
	TelegramID int64
	Username   string
	FirstName  string
	LastName   string
	Timezone   string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Workspace struct {
	ID          int64
	Name        string
	OwnerUserID int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Task struct {
	ID             int64
	WorkspaceID    int64
	CreatorUserID  int64
	AssigneeUserID *int64
	Title          string
	Description    *string
	Status         Status
	Priority       Priority
	Category       *string
	RecurrenceRule *RecurrenceRule
	DueAt          *time.Time
	RemindAt       *time.Time
	PostponedCount int
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DoneAt         *time.Time
	CancelledAt    *time.Time
}

type TaskEvent struct {
	ID        int64
	TaskID    int64
	UserID    int64
	EventType string
	Payload   string
	CreatedAt time.Time
}

type TaskReminder struct {
	ID        int64
	TaskID    int64
	RemindAt  time.Time
	SentAt    *time.Time
	CreatedAt time.Time
}

type TelegramUser struct {
	TelegramID int64
	Username   string
	FirstName  string
	LastName   string
}
