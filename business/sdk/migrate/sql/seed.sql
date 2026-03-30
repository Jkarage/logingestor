INSERT INTO users (
		id,
		name,
		email,
		roles,
		password_hash,
		enabled,
		date_created,
		date_updated
	)
VALUES (
		'231c6f21-0207-4d5c-bc83-a4fdbd5cb06f',
		'Alfie Solomon',
		'alfie@logingestor.com',
		'{SUPER ADMIN}',
		'$2a$10$1ggfMVZV6Js0ybvJufLRUOWHS5f6KneuP0XwwHpJ8L8ipdry9f2/a',
		true,
		'2019-03-24 00:00:00',
		'2019-03-24 00:00:00'
	),
	(
		'45b5fbd3-755f-4379-8f07-a58d4a30fa2f',
		'User Gopher',
		'user@example.com',
		'{VIEWER}',
		'$2a$10$9/XASPKBbJKVfCAZKDH.UuhsuALDr5vVm6VrYA9VFR8rccK86C1hW',
		true,
		'2019-03-24 00:00:00',
		'2019-03-24 00:00:00'
	) ON CONFLICT DO NOTHING;