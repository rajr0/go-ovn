/**
 * Copyright (c) 2017 eBay Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *  http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 **/

package goovn

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/ebay/libovsdb"
)

var (
	ErrorSchema   = errors.New("table schema error")
	ErrorNotFound = errors.New("object not found")
	ErrorExist    = errors.New("object exist")
)

type OVNRow map[string]interface{}

func (odbi *ovndb) getRowUUIDs(table string, row OVNRow) []string {
	var uuids []string
	var wildcard bool

	if reflect.DeepEqual(row, make(OVNRow)) {
		wildcard = true
	}

	odbi.cachemutex.RLock()
	defer odbi.cachemutex.RUnlock()

	cacheTable, ok := odbi.cache[table]
	if !ok {
		return nil
	}

	for uuid, drows := range cacheTable {
		if wildcard {
			uuids = append(uuids, uuid)
			continue
		}

		found := false
		for field, value := range row {
			if v, ok := drows.Fields[field]; ok {
				if v == value {
					found = true
				} else {
					found = false
					break
				}
			}
		}
		if found {
			uuids = append(uuids, uuid)
		}
	}

	return uuids
}

func (odbi *ovndb) getRowUUID(table string, row OVNRow) string {
	uuids := odbi.getRowUUIDs(table, row)
	if len(uuids) > 0 {
		return uuids[0]
	}
	return ""
}

//test if map s contains t
//This function is not both s and t are nil at same time
func (odbi *ovndb) oMapContians(s, t map[interface{}]interface{}) bool {
	if s == nil || t == nil {
		return false
	}

	for tk, tv := range t {
		if sv, ok := s[tk]; !ok {
			return false
		} else if tv != sv {
			return false
		}
	}
	return true
}

func (odbi *ovndb) getRowUUIDContainsUUID(table, field, uuid string) (string, error) {
	odbi.cachemutex.RLock()
	defer odbi.cachemutex.RUnlock()

	cacheTable, ok := odbi.cache[table]
	if !ok {
		return "", ErrorSchema
	}

	for id, drows := range cacheTable {
		v := fmt.Sprintf("%s", drows.Fields[field])
		if strings.Contains(v, uuid) {
			return id, nil
		}
	}
	return "", ErrorNotFound
}

func (odbi *ovndb) getRowsMatchingUUID(table, field, uuid string) ([]string, error) {
	odbi.cachemutex.Lock()
	defer odbi.cachemutex.Unlock()
	var uuids []string
	for id, drows := range odbi.cache[table] {
		v := fmt.Sprintf("%s", drows.Fields[field])
		if strings.Contains(v, uuid) {
			uuids = append(uuids, id)
		}
	}
	if len(uuids) == 0 {
		return uuids, ErrorNotFound
	}
	return uuids, nil
}

func (odbi *ovndb) transact(ops ...libovsdb.Operation) ([]libovsdb.OperationResult, error) {
	// Only support one trans at same time now.
	odbi.tranmutex.Lock()
	defer odbi.tranmutex.Unlock()
	reply, err := odbi.client.Transact(dbNB, ops...)

	if err != nil {
		return reply, err
	}

	if len(reply) < len(ops) {
		for i, o := range reply {
			if o.Error != "" && i < len(ops) {
				return nil, fmt.Errorf("Transaction Failed due to an error : %v details: %v in %v", o.Error, o.Details, ops[i])
			}
		}
		return reply, fmt.Errorf("Number of Replies should be atleast equal to number of operations")
	}
	return reply, nil
}

func (odbi *ovndb) execute(cmds ...*OvnCommand) error {
	if cmds == nil {
		return nil
	}
	var ops []libovsdb.Operation
	for _, cmd := range cmds {
		if cmd != nil {
			ops = append(ops, cmd.Operations...)
		}
	}
	_, err := odbi.transact(ops...)
	if err != nil {
		return err
	}
	return nil
}

func (odbi *ovndb) float64_to_int(row libovsdb.Row) {
	for field, value := range row.Fields {
		if v, ok := value.(float64); ok {
			n := int(v)
			if float64(n) == v {
				row.Fields[field] = n
			}
		}
	}
}

func (odbi *ovndb) populateCache(updates libovsdb.TableUpdates) {
	empty := libovsdb.Row{}

	odbi.cachemutex.Lock()
	defer odbi.cachemutex.Unlock()

	for table, tableUpdate := range updates.Updates {
		if _, ok := odbi.cache[table]; !ok {
			odbi.cache[table] = make(map[string]libovsdb.Row)
		}
		for uuid, row := range tableUpdate.Rows {
			// TODO: this is a workaround for the problem of
			// missing json number conversion in libovsdb
			odbi.float64_to_int(row.New)

			if !reflect.DeepEqual(row.New, empty) {
				odbi.cache[table][uuid] = row.New
				if odbi.callback != nil {
					switch table {
					case tableLogicalRouter:
						lr := odbi.rowToLogicalRouter(uuid)
						odbi.callback.OnLogicalRouterCreate(lr)
					case tableLogicalRouterPort:
						lrp := odbi.rowToLogicalRouterPort(uuid)
						odbi.callback.OnLogicalRouterPortCreate(lrp)
					case tableLogicalSwitch:
						ls := odbi.rowToLogicalSwitch(uuid)
						odbi.callback.OnLogicalSwitchCreate(ls)
					case tableLogicalSwitchPort:
						lp := odbi.rowToLogicalPort(uuid)
						odbi.callback.OnLogicalPortCreate(lp)
					case tableACL:
						acl := odbi.rowToACL(uuid)
						odbi.callback.OnACLCreate(acl)
					case tableDHCPOptions:
						dhcp := odbi.rowToDHCPOptions(uuid)
						odbi.callback.OnDHCPOptionsCreate(dhcp)
					case tableQoS:
						qos := odbi.rowToQoS(uuid)
						odbi.callback.OnQoSCreate(qos)
					case tableLoadBalancer:
						lb, _ := odbi.rowToLB(uuid)
						odbi.callback.OnLoadBalancerCreate(lb)
					}

				}
			} else {
				if odbi.callback != nil {
					switch table {
					case tableLogicalRouter:
						lr := odbi.rowToLogicalRouter(uuid)
						odbi.callback.OnLogicalRouterDelete(lr)
					case tableLogicalRouterPort:
						lrp := odbi.rowToLogicalRouterPort(uuid)
						odbi.callback.OnLogicalRouterPortDelete(lrp)
					case tableLogicalSwitch:
						ls := odbi.rowToLogicalSwitch(uuid)
						odbi.callback.OnLogicalSwitchDelete(ls)
					case tableLogicalSwitchPort:
						lp := odbi.rowToLogicalPort(uuid)
						odbi.callback.OnLogicalPortDelete(lp)
					case tableACL:
						acl := odbi.rowToACL(uuid)
						odbi.callback.OnACLDelete(acl)
					case tableDHCPOptions:
						dhcp := odbi.rowToDHCPOptions(uuid)
						odbi.callback.OnDHCPOptionsDelete(dhcp)
					case tableQoS:
						qos := odbi.rowToQoS(uuid)
						odbi.callback.OnQoSDelete(qos)
					case tableLoadBalancer:
						lb, _ := odbi.rowToLB(uuid)
						odbi.callback.OnLoadBalancerDelete(lb)
					}
				}
				delete(odbi.cache[table], uuid)
			}
		}
	}
}

func (odbi *ovndb) ConvertGoSetToStringArray(oset libovsdb.OvsSet) []string {
	var ret = []string{}
	for _, s := range oset.GoSet {
		value, ok := s.(string)
		if ok {
			ret = append(ret, value)
		}
	}
	return ret
}
