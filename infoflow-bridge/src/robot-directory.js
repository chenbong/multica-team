// Only this robot's SDK-verified public identity is published. Failed reads are
// unknown, never evidence that a configured robot or another account is absent.
export async function pollRobotDirectory(relay, scope) {
  const metadata = await relay.privateBindingMetadata({ includeDisabled: true });
  if (relay.stopped || !metadata.robot_name || !metadata.robot_chat_id) return;
  await relay.request("robot-directory", {
    ...scope, robot_name: metadata.robot_name, robot_chat_id: metadata.robot_chat_id,
    private_enabled: metadata.private_enabled === true && Array.isArray(relay.config.allowUsers) &&
      relay.config.allowUsers.length > 0,
    available: relay.getRobotAvailable() === true,
  });
}
