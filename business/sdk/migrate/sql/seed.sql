INSERT INTO integration_providers (id, name, icon, type, description, fields, sort_order) VALUES
  ('slack',     'Slack',     '💬', 'Messaging', 'Send alerts to Slack channels via webhook.',
   '[{"k":"webhookUrl","label":"Webhook URL","ph":"https://hooks.slack.com/services/..."}]', 1),
  ('discord',   'Discord',   '🎮', 'Messaging', 'Forward log alerts to Discord via webhook.',
   '[{"k":"webhookUrl","label":"Webhook URL","ph":"https://discord.com/api/webhooks/..."}]', 2),
  ('telegram',  'Telegram',  '✈️', 'Messaging', 'Receive alerts as Telegram bot messages.',
   '[{"k":"botToken","label":"Bot Token","ph":"123456:ABC..."},{"k":"chatId","label":"Chat ID","ph":"-100123"}]', 3),
  ('pagerduty', 'PagerDuty', '🚨', 'Incident',  'Auto-create PagerDuty incidents on critical errors.',
   '[{"k":"apiKey","label":"API Key","ph":"u+xxxxxxxx"},{"k":"serviceId","label":"Service ID","ph":"P1234AB"}]', 4),
  ('webhook',   'Webhook',   '🔗', 'Custom',    'POST structured JSON to any HTTP endpoint.',
   '[{"k":"url","label":"Target URL","ph":"https://yourapp.com/hook"},{"k":"secret","label":"Secret","ph":"optional HMAC secret"}]', 5),
  ('email',     'Email',     '📧', 'Notify',    'Send email alerts when log events trigger.',
   '[{"k":"to","label":"To Address","ph":"team@co.com"}]', 6),
  ('opsgenie',  'OpsGenie',  '🔔', 'Incident',  'Create OpsGenie alerts for on-call escalation.',
   '[{"k":"apiKey","label":"API Key","ph":"xxxx-xxxx-xxxx"}]', 7),
  ('jira',      'Jira',      '🧩', 'Ticketing', 'Open Jira issues automatically on ERROR logs.',
   '[{"k":"domain","label":"Domain","ph":"org.atlassian.net"},{"k":"email","label":"Account Email","ph":"you@org.com"},{"k":"token","label":"API Token","ph":"ATATT..."},{"k":"project","label":"Project Key","ph":"ENG"}]', 8)
ON CONFLICT (id) DO NOTHING;

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