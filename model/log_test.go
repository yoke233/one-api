package model

import (
	"reflect"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetAllLogsPaginatesNewestFirst(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:log-order-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatalf("migrate logs: %v", err)
	}

	previousLogDB := LOG_DB
	LOG_DB = db
	t.Cleanup(func() {
		LOG_DB = previousLogDB
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	records := []Log{
		{Id: 1, CreatedAt: 100},
		{Id: 2, CreatedAt: 300},
		{Id: 3, CreatedAt: 200},
		{Id: 4, CreatedAt: 300},
	}
	if err := db.Create(&records).Error; err != nil {
		t.Fatalf("insert logs: %v", err)
	}

	firstPage, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 0, 2, 0)
	if err != nil {
		t.Fatalf("get first page: %v", err)
	}
	secondPage, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 2, 2, 0)
	if err != nil {
		t.Fatalf("get second page: %v", err)
	}
	if len(firstPage) != 2 || len(secondPage) != 2 {
		t.Fatalf("unexpected page sizes: first=%d second=%d", len(firstPage), len(secondPage))
	}

	ids := []int{firstPage[0].Id, firstPage[1].Id, secondPage[0].Id, secondPage[1].Id}
	if want := []int{4, 2, 3, 1}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("logs are not globally ordered newest first: got %v, want %v", ids, want)
	}
}
