// universalDataStore — shim that delegates to enhancedUniversalDataStore.
//
// Both stores were running independent setInterval trees (30 s vs 60 s per page),
// causing every API endpoint to be polled twice simultaneously. The enhanced store
// is the authoritative implementation; this file re-exports everything under the
// original names so existing import sites require no changes.

export {
  useEnhancedUniversalDataStore as useUniversalDataStore,
  initializeEnhancedDataLoading as initializeDataLoading,
  initializeEnhancedDataLoading as initializeUniversalDataLoading,
  cleanupEnhancedDataLoading    as cleanupUniversalDataLoading,
  selectDashboardData,
  selectAIChatData,
  selectAdminData,
  selectAnalyticsData,
  selectIncidentsData,
  selectIntegrationsData,
  selectSettingsData,
} from '@/stores/enhancedUniversalDataStore'

import type { EnhancedUniversalDataStore } from '@/stores/enhancedUniversalDataStore'

export const selectIsInitialized = (state: EnhancedUniversalDataStore) => state.isInitialized
export const selectPageErrors    = (state: EnhancedUniversalDataStore) => state.globalErrors
