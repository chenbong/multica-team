# Per-conversation task sync

The task header has one conversation-management entry. Opening a bound group or
private chat and changing its sync setting happen inside the corresponding tab.
There are no external-chat shortcuts or sync summaries in the task header.

New and existing bindings default to enabled. Each binding's persisted switch
controls outbound task comments, agent replies, progress and completion content.
Group and private settings are independent. Incoming text/images and operation
receipts keep working while outbound task content is paused.

Human binding managers may change group sync. Private sync is restricted to the
owner of that private binding, even for workspace admins. Updates require the
current binding generation; agent and daemon credentials cannot change settings.

Pausing marks unsent delivery rows cancelled, retaining audit/idempotency records.
Polling and the final send-admission check both enforce the setting. Resumption
advances the broadcast cutoff; neither paused content nor cancelled deliveries
can replay. Repeating an enabled-setting update does not advance the cutoff.
Already-sent/in-flight messages cannot be recalled. Switching itself produces
no InfoFlow notification; binding/unbinding and command/failure receipts remain.

The additive migration keeps all pre-existing bindings enabled. No daemon or CLI
update is needed. Rollback must not restore an old database snapshot over newer
task activity. An older API does not enforce sync settings, so do not roll back
while users have paused destinations without first protecting outbound delivery.
