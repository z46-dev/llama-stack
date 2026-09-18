package resource

import (
	"fmt"
	"os"
	"time"

	"github.com/z46-dev/gosqlite"
)

type (
	capabilityRecord struct {
		Hash        string    `gosqlite:"Hash,primary,unique"`
		ID          string    `gosqlite:"ID,unique"`
		UserID      string    `gosqlite:"UserID"`
		RunID       string    `gosqlite:"RunID"`
		WorkspaceID string    `gosqlite:"WorkspaceID"`
		TargetHost  string    `gosqlite:"TargetHost"`
		ExpiresAt   time.Time `gosqlite:"ExpiresAt"`
	}

	leaseRecord struct {
		ID             string    `gosqlite:"ID,primary,unique"`
		CapabilityHash string    `gosqlite:"CapabilityHash,fkey:capabilityRecord.Hash,ondelete:cascade"`
		UserID         string    `gosqlite:"UserID"`
		RunID          string    `gosqlite:"RunID"`
		WorkspaceID    string    `gosqlite:"WorkspaceID"`
		TargetHost     string    `gosqlite:"TargetHost"`
		Ports          []Port    `gosqlite:"Ports"`
		CreatedAt      time.Time `gosqlite:"CreatedAt"`
		ExpiresAt      time.Time `gosqlite:"ExpiresAt"`
	}

	store struct {
		driver       *gosqlite.Driver
		capabilities *gosqlite.RegisteredStruct[capabilityRecord]
		leases       *gosqlite.RegisteredStruct[leaseRecord]
	}
)

func openStore(path string) (database *store, err error) {
	database = &store{}
	if database.driver, err = gosqlite.Begin(path); err != nil {
		return
	}
	if database.capabilities, err = gosqlite.Register(database.driver, capabilityRecord{}); err != nil {
		_ = database.driver.Close()
		return nil, err
	}
	if database.leases, err = gosqlite.Register(database.driver, leaseRecord{}); err != nil {
		_ = database.driver.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0o640); err != nil {
		_ = database.driver.Close()
		return nil, fmt.Errorf("protect resource database: %w", err)
	}
	return
}

func (database *store) close() (err error) {
	err = database.driver.Close()
	return
}

func (database *store) insertCapability(capability Capability) (err error) {
	err = database.capabilities.Insert(&capabilityRecord{
		Hash:        capability.Hash,
		ID:          capability.ID,
		UserID:      capability.UserID,
		RunID:       capability.RunID,
		WorkspaceID: capability.WorkspaceID,
		TargetHost:  capability.TargetHost,
		ExpiresAt:   capability.ExpiresAt,
	})
	return
}

func (database *store) capabilityByTokenHash(hash string, now time.Time) (capability Capability, err error) {
	var records []*capabilityRecord

	records, err = database.capabilities.SelectAllWithFilter(
		gosqlite.NewFilter().
			KeyCmp(database.capabilities.FieldByGoName("Hash"), gosqlite.OpEqual, hash).
			And().
			KeyCmp(database.capabilities.FieldByGoName("ExpiresAt"), gosqlite.OpGreaterThan, now).
			Limit(1),
	)
	if err == nil && len(records) == 0 {
		err = ErrForbidden
	} else if err == nil {
		capability = capabilityFromRecord(records[0])
	}
	return
}

func (database *store) capabilityByID(identifier string) (record *capabilityRecord, err error) {
	var records []*capabilityRecord

	records, err = database.capabilities.SelectAllWithFilter(
		gosqlite.NewFilter().KeyCmp(database.capabilities.FieldByGoName("ID"), gosqlite.OpEqual, identifier).Limit(1),
	)
	if err == nil && len(records) == 0 {
		err = ErrNotFound
	} else if err == nil {
		record = records[0]
	}
	return
}

func (database *store) deleteCapability(record *capabilityRecord) (err error) {
	err = database.capabilities.Delete(record.Hash)
	return
}

func (database *store) deleteExpiredCapabilities(now time.Time) (err error) {
	_, err = database.capabilities.DeleteWithFilter(
		gosqlite.NewFilter().KeyCmp(database.capabilities.FieldByGoName("ExpiresAt"), gosqlite.OpLessThanOrEqual, now),
	)
	return
}

func (database *store) insertLease(lease Lease, capabilityHash string) (err error) {
	err = database.leases.Insert(&leaseRecord{
		ID:             lease.ID,
		CapabilityHash: capabilityHash,
		UserID:         lease.UserID,
		RunID:          lease.RunID,
		WorkspaceID:    lease.WorkspaceID,
		TargetHost:     lease.TargetHost,
		Ports:          lease.Ports,
		CreatedAt:      lease.CreatedAt,
		ExpiresAt:      lease.ExpiresAt,
	})
	return
}

func (database *store) activeLeases(now time.Time) (records []*leaseRecord, err error) {
	records, err = database.leases.SelectAllWithFilter(
		gosqlite.NewFilter().
			KeyCmp(database.leases.FieldByGoName("ExpiresAt"), gosqlite.OpGreaterThan, now).
			Ordering(database.leases.FieldByGoName("CreatedAt"), true),
	)
	return
}

func (database *store) leasesForCapability(hash string, now time.Time) (records []*leaseRecord, err error) {
	records, err = database.leases.SelectAllWithFilter(
		gosqlite.NewFilter().
			KeyCmp(database.leases.FieldByGoName("CapabilityHash"), gosqlite.OpEqual, hash).
			And().
			KeyCmp(database.leases.FieldByGoName("ExpiresAt"), gosqlite.OpGreaterThan, now).
			Ordering(database.leases.FieldByGoName("CreatedAt"), true),
	)
	return
}

func (database *store) leaseByID(identifier string) (record *leaseRecord, err error) {
	var records []*leaseRecord

	records, err = database.leases.SelectAllWithFilter(
		gosqlite.NewFilter().KeyCmp(database.leases.FieldByGoName("ID"), gosqlite.OpEqual, identifier).Limit(1),
	)
	if err == nil && len(records) == 0 {
		err = ErrNotFound
	} else if err == nil {
		record = records[0]
	}
	return
}

func (database *store) updateLease(record *leaseRecord) (err error) {
	err = database.leases.Update(record)
	return
}

func (database *store) deleteLease(identifier string) (err error) {
	if _, err = database.leaseByID(identifier); err != nil {
		return
	}
	err = database.leases.Delete(identifier)
	return
}

func (database *store) expiredLeases(now time.Time) (records []*leaseRecord, err error) {
	records, err = database.leases.SelectAllWithFilter(
		gosqlite.NewFilter().KeyCmp(database.leases.FieldByGoName("ExpiresAt"), gosqlite.OpLessThanOrEqual, now),
	)
	return
}

func (database *store) deleteExpiredLeases(now time.Time) (err error) {
	_, err = database.leases.DeleteWithFilter(
		gosqlite.NewFilter().KeyCmp(database.leases.FieldByGoName("ExpiresAt"), gosqlite.OpLessThanOrEqual, now),
	)
	return
}

func capabilityFromRecord(record *capabilityRecord) (capability Capability) {
	capability = Capability{
		ID:          record.ID,
		Hash:        record.Hash,
		UserID:      record.UserID,
		RunID:       record.RunID,
		WorkspaceID: record.WorkspaceID,
		TargetHost:  record.TargetHost,
		ExpiresAt:   record.ExpiresAt,
	}
	return
}

func leaseFromRecord(record *leaseRecord) (lease Lease) {
	lease = Lease{
		ID:          record.ID,
		UserID:      record.UserID,
		RunID:       record.RunID,
		WorkspaceID: record.WorkspaceID,
		TargetHost:  record.TargetHost,
		Ports:       record.Ports,
		CreatedAt:   record.CreatedAt,
		ExpiresAt:   record.ExpiresAt,
	}
	return
}

func leasesFromRecords(records []*leaseRecord) (leases []Lease) {
	for _, record := range records {
		leases = append(leases, leaseFromRecord(record))
	}
	return
}
