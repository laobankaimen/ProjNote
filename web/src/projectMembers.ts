import { ref } from 'vue'

// A roster may be edited from a nested picker or from LAN settings. Refresh
// every open picker so that the next assignment uses the current member IDs.
export const projectMembersRevision = ref(0)
export function membersChanged() { projectMembersRevision.value++ }
