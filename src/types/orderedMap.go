package types

// OrderedMap holds the key-value pairs and the key order.
// K must be comparable, V can be any type.
type OrderedMap[K comparable, V any] struct {
	keys []K
	vals map[K]V
}

// NewOrderedMap creates and returns a new OrderedMap.
func NewOrderedMap[K comparable, V any]() *OrderedMap[K, V] {
	return &OrderedMap[K, V]{
		keys: make([]K, 0),
		vals: make(map[K]V),
	}
}

// Set adds or updates a key-value pair. It preserves insertion order.
func (om *OrderedMap[K, V]) Set(key K, value V) {
	if _, exists := om.vals[key]; !exists {
		om.keys = append(om.keys, key) // Add new key to the end of the slice
	}
	om.vals[key] = value // Add/update the value in the map
}

// Get retrieves a value by its key.
func (om *OrderedMap[K, V]) Get(key K) (V, bool) {
	val, exists := om.vals[key]
	return val, exists
}

// Drop removes a key-value pair from the map.
// It returns true if the key was found and deleted, false otherwise.
func (om *OrderedMap[K, V]) Drop(key K) bool {
	if _, exists := om.vals[key]; !exists {
		return false
	}

	delete(om.vals, key)

	for i, k := range om.keys {
		if k == key {
			om.keys = append(om.keys[:i], om.keys[i+1:]...)
			break
		}
	}

	return true
}

// Oldest returns the first inserted key-value pair still in the map.
func (om *OrderedMap[K, V]) Oldest() (K, V, bool) {
	if len(om.keys) == 0 {
		var key K
		var val V
		return key, val, false
	}
	key := om.keys[0]
	return key, om.vals[key], true
}

// Len returns the number of items in the map.
func (om *OrderedMap[K, V]) Len() int {
	return len(om.keys)
}

// Keys returns the slice of keys in their original insertion order.
func (om *OrderedMap[K, V]) Keys() []K {
	return om.keys
}

// Values returns a slice of values in the order of their corresponding keys.
func (om *OrderedMap[K, V]) Values() []V {
	values := make([]V, 0, len(om.keys))
	for _, key := range om.keys {
		values = append(values, om.vals[key])
	}
	return values
}
