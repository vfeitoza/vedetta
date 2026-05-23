package storage

// NotificationPref represents a per-user, per-camera, per-class notification opt-out.
// Only disabled rows are stored; missing rows mean enabled.
type NotificationPref struct {
	Username    string
	Camera      string
	ObjectClass string
	Enabled     bool
}

// IsNotificationEnabled returns true unless an explicit disable row exists for
// (username, camera, class) or (username, camera, "*").
func (d *DB) IsNotificationEnabled(username, camera, class string) (bool, error) {
	var count int
	err := d.db.QueryRow(`
		SELECT COUNT(*) FROM notification_prefs
		 WHERE username = ? AND camera = ?
		   AND (object_class = ? OR object_class = '*')
		   AND enabled = 0`,
		username, camera, class).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

// SetNotificationPref sets or unsets a pref row.
// To keep the table sparse, an enabled=true call DELETEs any existing row (default is enabled).
// A false call INSERTs or UPDATEs the row with enabled=0.
func (d *DB) SetNotificationPref(username, camera, class string, enabled bool) error {
	if enabled {
		_, err := d.db.Exec(`
			DELETE FROM notification_prefs
			 WHERE username = ? AND camera = ? AND object_class = ?`,
			username, camera, class)
		return err
	}
	_, err := d.db.Exec(`
		INSERT INTO notification_prefs (username, camera, object_class, enabled)
		VALUES (?, ?, ?, 0)
		ON CONFLICT(username, camera, object_class) DO UPDATE SET enabled = 0`,
		username, camera, class)
	return err
}

// ListNotificationPrefs returns all disabled rows for a user (enabled rows aren't stored).
func (d *DB) ListNotificationPrefs(username string) ([]NotificationPref, error) {
	rows, err := d.db.Query(`
		SELECT username, camera, object_class, enabled
		  FROM notification_prefs WHERE username = ? ORDER BY camera, object_class`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationPref
	for rows.Next() {
		var p NotificationPref
		if err := rows.Scan(&p.Username, &p.Camera, &p.ObjectClass, &p.Enabled); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
