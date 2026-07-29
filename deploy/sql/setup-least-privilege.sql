-- =============================================================================
-- URA least-privilege SQL Server access setup (run once per instance, as a
-- sysadmin, BEFORE enabling deep SQL telemetry). Strictly read-only.
--
-- What the agent needs and why:
--   VIEW SERVER STATE            server-level DMVs: dm_os_process_memory,
--                                dm_os_sys_info, dm_os_performance_counters,
--                                dm_os_memory_clerks, dm_os_wait_stats,
--                                dm_io_virtual_file_stats, dm_exec_requests,
--                                dm_exec_sessions, dm_exec_query_memory_grants,
--                                dm_hadr_* (AG topology)
--   VIEW ANY DEFINITION          sys.configurations values, sys.master_files
--   SELECT on msdb job tables    SQL Agent job correlation (optional)
--
-- SQL Server 2022+ note: the more granular VIEW SERVER PERFORMANCE STATE
-- permission covers every performance DMV the agent uses and is preferred
-- over the broader VIEW SERVER STATE — use the 2022+ block below when
-- available. On 2016–2019, VIEW SERVER STATE is the least available grant.
--
-- The agent NEVER writes, never reads user tables, and never selects query
-- text. Revoke everything with the teardown block at the bottom.
-- =============================================================================

-- ---------- 1. Choose the authentication model ----------

-- (a) Windows integrated auth (PREFERRED on Windows): the agent service
--     account needs a login. Default service account shown; adjust if the
--     installer used a different one.
--   CREATE LOGIN [NT SERVICE\ura-agent] FROM WINDOWS;

-- (b) SQL login (Linux, or Windows without integrated auth). The password is
--     stored ONLY in the root-owned 0600 file referenced by
--     sql.password_file — never in the agent configuration.
--   CREATE LOGIN [ura_readonly] WITH PASSWORD = '<generate-a-long-random-password>',
--        CHECK_POLICY = ON, CHECK_EXPIRATION = OFF;

-- Uncomment ONE of the above, then set @login accordingly:
DECLARE @login sysname = N'ura_readonly';  -- or N'NT SERVICE\ura-agent'
DECLARE @sql   nvarchar(max);

-- ---------- 2. Server-level read-only grants ----------

-- SQL Server 2016–2019 (and always valid):
SET @sql = N'GRANT VIEW SERVER STATE TO ' + QUOTENAME(@login) + N';';
EXEC (@sql);
SET @sql = N'GRANT VIEW ANY DEFINITION TO ' + QUOTENAME(@login) + N';';
EXEC (@sql);

-- SQL Server 2022+ (optional, more granular — replaces VIEW SERVER STATE):
-- SET @sql = N'GRANT VIEW SERVER PERFORMANCE STATE TO ' + QUOTENAME(@login) + N';';
-- EXEC (@sql);
-- SET @sql = N'REVOKE VIEW SERVER STATE FROM ' + QUOTENAME(@login) + N';';
-- EXEC (@sql);

-- ---------- 3. Optional: SQL Agent job correlation (msdb, read-only) ----------
SET @sql = N'USE msdb;
IF NOT EXISTS (SELECT 1 FROM sys.database_principals WHERE name = ''' + @login + N''')
    CREATE USER ' + QUOTENAME(@login) + N' FOR LOGIN ' + QUOTENAME(@login) + N';
GRANT SELECT ON msdb.dbo.sysjobs        TO ' + QUOTENAME(@login) + N';
GRANT SELECT ON msdb.dbo.sysjobactivity TO ' + QUOTENAME(@login) + N';
GRANT SELECT ON msdb.dbo.syssessions    TO ' + QUOTENAME(@login) + N';';
EXEC (@sql);

-- ---------- 4. Optional: tempdb space usage ----------
-- tempdb.sys.dm_db_file_space_usage is covered by VIEW SERVER STATE; no
-- extra grant needed.

PRINT 'URA least-privilege setup complete for login: ' + @login;
GO

-- =============================================================================
-- TEARDOWN (run at the end of the monitoring engagement)
-- =============================================================================
-- DECLARE @login sysname = N'ura_readonly';
-- DECLARE @sql nvarchar(max);
-- SET @sql = N'USE msdb; IF EXISTS (SELECT 1 FROM sys.database_principals WHERE name = '''+@login+N''') DROP USER ' + QUOTENAME(@login) + N';';
-- EXEC (@sql);
-- SET @sql = N'REVOKE VIEW SERVER STATE FROM ' + QUOTENAME(@login) + N';'; EXEC (@sql);
-- SET @sql = N'REVOKE VIEW ANY DEFINITION FROM ' + QUOTENAME(@login) + N';'; EXEC (@sql);
-- SET @sql = N'DROP LOGIN ' + QUOTENAME(@login) + N';'; EXEC (@sql);
