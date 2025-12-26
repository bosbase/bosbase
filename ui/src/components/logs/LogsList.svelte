<script>
    import { _ } from 'svelte-i18n';
    import Scroller from "@/components/base/Scroller.svelte";
    import SortHeader from "@/components/base/SortHeader.svelte";
    import LogDate from "@/components/logs/LogDate.svelte";
    import LogLevel from "@/components/logs/LogLevel.svelte";
    import ApiClient from "@/utils/ApiClient";
    import CommonHelper from "@/utils/CommonHelper";
    import { createEventDispatcher } from "svelte";
    import { fly } from "svelte/transition";

    const dispatch = createEventDispatcher();

    const perPage = 50;

    export let filter = "";
    export let presets = "";
    export let zoom = {};
    export let sort = "-created";

    let logs = [];
    let currentPage = 1;
    let totalPages = 1;
    let totalItems = 0;
    let isLoading = false;
    let bulkSelected = {};

    $: if (
        typeof sort !== "undefined" ||
        typeof filter !== "undefined" ||
        typeof presets !== "undefined" ||
        typeof zoom !== "undefined"
    ) {
        clearList();
        load(1);
    }

    $: totalBulkSelected = Object.keys(bulkSelected).length;

    $: areAllLogsSelected = logs.length && totalBulkSelected === logs.length;

    $: hasPrevPage = currentPage > 1;

    $: hasNextPage = currentPage < totalPages;

    export async function load(page = 1) {
        isLoading = true;

        const normalizedFilter = [presets, CommonHelper.normalizeLogsFilter(filter)];

        if (zoom.min && zoom.max) {
            normalizedFilter.push(`created >= "${zoom.min}" && created <= "${zoom.max}"`);
        }

        return ApiClient.logs
            .getList(page, perPage, {
                sort: sort,
                filter: normalizedFilter
                    .filter(Boolean)
                    .map((f) => "(" + f + ")")
                    .join("&&"),
            })
            .then((result) => {
                const items = CommonHelper.toArray(result.items);

                isLoading = false;
                currentPage = result.page;
                totalPages = result.totalPages || 1;
                totalItems = result.totalItems ?? items.length;

                logs = items;
                bulkSelected = {};

                dispatch("load", logs);
            })
            .catch((err) => {
                if (!err?.isAbort) {
                    isLoading = false;
                    console.warn(err);
                    clearList();
                    ApiClient.error(err, !normalizedFilter || err?.status != 400); // silence filter errors
                }
            });
    }

    function clearList() {
        logs = [];
        bulkSelected = {};
        currentPage = 1;
        totalPages = 1;
        totalItems = 0;
    }

    function goToPage(page) {
        if (isLoading || page === currentPage || page < 1 || page > totalPages) {
            return;
        }

        load(page);
    }

    function goToFirstPage() {
        goToPage(1);
    }

    function goToLastPage() {
        goToPage(totalPages);
    }

    function goToPreviousPage() {
        goToPage(currentPage - 1);
    }

    function goToNextPage() {
        goToPage(currentPage + 1);
    }

    function toggleSelectAllLogs() {
        if (areAllLogsSelected) {
            deselectAllLogs();
        } else {
            selectAllLogs();
        }
    }

    function deselectAllLogs() {
        bulkSelected = {};
    }

    function selectAllLogs() {
        for (const log of logs) {
            bulkSelected[log.id] = log;
        }

        bulkSelected = bulkSelected;
    }

    function toggleSelectLog(log) {
        if (!bulkSelected[log.id]) {
            bulkSelected[log.id] = log;
        } else {
            delete bulkSelected[log.id];
        }

        bulkSelected = bulkSelected; // trigger reactivity
    }

    const dateFilenameRegex = /[-:\. ]/gi;

    function downloadSelected() {
        // extract the bulk selected log objects sorted desc
        const selected = Object.values(bulkSelected).sort((a, b) => {
            if (a.created < b.created) {
                return 1;
            }

            if (a.created > b.created) {
                return -1;
            }

            return 0;
        });

        if (!selected.length) {
            return; // nothing to download
        }

        if (selected.length == 1) {
            return CommonHelper.downloadJson(
                selected[0],
                "log_" + selected[0].created.replaceAll(dateFilenameRegex, "") + ".json",
            );
        }

        const to = selected[0].created.replaceAll(dateFilenameRegex, "");
        const from = selected[selected.length - 1].created.replaceAll(dateFilenameRegex, "");

        return CommonHelper.downloadJson(selected, `${selected.length}_logs_${from}_to_${to}.json`);
    }

    function getLogPreviewKeys(log) {
        let keys = [];

        if (!log.data) {
            return keys;
        }

        if (log.data.type == "request") {
            const requestKeys = ["status", "execTime", "auth", "authId", "userIP"];
            for (let key of requestKeys) {
                if (typeof log.data[key] != "undefined") {
                    keys.push({ key });
                }
            }

            // add the referer if it is from a different source
            if (log.data.referer && !log.data.referer.includes(window.location.host)) {
                keys.push({ key: "referer" });
            }

            if (log.data.body && Object.keys(log.data.body || {}).length) {
                keys.push({ key: "body" });
            }
        } else {
            // extract the first 6 keys (excluding the error and details)
            const allKeys = Object.keys(log.data);
            for (const key of allKeys) {
                if (key != "error" && key != "details" && keys.length < 6) {
                    keys.push({ key });
                }
            }
        }

        // ensure that the error and detail keys are last
        if (log.data.error) {
            keys.push({ key: "error", label: "label-danger" });
        }
        if (log.data.details) {
            keys.push({ key: "details", label: "label-warning" });
        }

        return keys;
    }
</script>

<Scroller class="table-wrapper">
    <table class="table" class:table-loading={isLoading}>
        <thead>
            <tr>
                <th class="bulk-select-col min-width">
                    {#if isLoading}
                        <span class="loader loader-sm" />
                    {:else}
                        <div class="form-field">
                            <input
                                type="checkbox"
                                id="checkbox_0"
                                disabled={!logs.length}
                                checked={areAllLogsSelected}
                                on:change={() => toggleSelectAllLogs()}
                            />
                            <label for="checkbox_0" />
                        </div>
                    {/if}
                </th>

                <SortHeader disable class="col-field-level min-width" name="level" bind:sort>
                    <div class="col-header-content">
                        <i class="ri-bookmark-line" />
                        <span class="txt">level</span>
                    </div>
                </SortHeader>

                <SortHeader disable class="col-type-text col-field-message" name="data" bind:sort>
                    <div class="col-header-content">
                        <i class="ri-file-list-2-line" />
                        <span class="txt">message</span>
                    </div>
                </SortHeader>

                <SortHeader disable class="col-type-text col-field-data" name="data" bind:sort>
                    <div class="col-header-content">
                        <i class="ri-braces-line" />
                        <span class="txt">data</span>
                    </div>
                </SortHeader>

                <SortHeader disable class="col-type-date col-field-created" name="created" bind:sort>
                    <div class="col-header-content">
                        <i class={CommonHelper.getFieldTypeIcon("date")} />
                        <span class="txt">created</span>
                    </div>
                </SortHeader>

                <th class="col-type-action min-width" />
            </tr>
        </thead>
        <tbody>
            {#each logs as log (log.id)}
                {@const isRequest = log.data?.type == "request"}
                {@const previewKeys = getLogPreviewKeys(log)}
                <tr
                    tabindex="0"
                    class="row-handle"
                    on:click={() => dispatch("select", log)}
                    on:keydown={(e) => {
                        if (e.code === "Enter") {
                            e.preventDefault();
                            dispatch("select", log);
                        }
                    }}
                >
                    <td class="bulk-select-col min-width">
                        <!-- svelte-ignore a11y-click-events-have-key-events -->
                        <!-- svelte-ignore a11y-no-static-element-interactions -->
                        <div class="form-field" on:click|stopPropagation>
                            <input
                                type="checkbox"
                                id="checkbox_{log.id}"
                                checked={bulkSelected[log.id]}
                                on:change={() => toggleSelectLog(log)}
                            />
                            <label for="checkbox_{log.id}" />
                        </div>
                    </td>

                    <td class="col-type-text col-field-level min-width">
                        <LogLevel level={log.level} />
                    </td>

                    <td class="col-type-text col-field-message">
                        <div class="flex flex-gap-10">
                            <span class="txt-ellipsis">{log.message}</span>
                        </div>
                        {#if previewKeys.length}
                            <div class="flex flex-wrap flex-gap-10 m-t-10">
                                {#each previewKeys as keyItem}
                                    <span class="label label-sm {keyItem.label || ''}">
                                        {#if isRequest && keyItem.key == "execTime"}
                                            {keyItem.key}: {log.data[keyItem.key]}ms
                                        {:else}
                                            {keyItem.key}: {CommonHelper.stringifyValue(
                                                log.data[keyItem.key],
                                                "N/A",
                                                80,
                                            )}
                                        {/if}
                                    </span>
                                {/each}
                            </div>
                        {/if}
                    </td>

                    <td class="col-type-text col-field-data">
                        <span class="txt-mono">
                            {CommonHelper.stringifyValue(log.data, "{}", 120)}
                        </span>
                    </td>

                    <td class="col-type-date col-field-created">
                        <LogDate date={log.created} />
                    </td>

                    <td class="col-type-action min-width">
                        <i class="ri-arrow-right-line" />
                    </td>
                </tr>
            {:else}
                {#if isLoading}
                    <tr>
                        <td colspan="99" class="p-xs">
                            <span class="skeleton-loader m-0" />
                        </td>
                    </tr>
                {:else}
                    <tr>
                        <td colspan="99" class="txt-center txt-hint p-xs">
                            <h6>No logs found.</h6>
                            {#if filter?.length}
                                <button
                                    type="button"
                                    class="btn btn-hint btn-expanded m-t-sm"
                                    on:click={() => (filter = "")}
                                >
                                    <span class="txt">Clear filters</span>
                                </button>
                            {/if}
                        </td>
                    </tr>
                {/if}
            {/each}
        </tbody>
    </table>
</Scroller>

{#if totalPages > 1}
    <div class="pagination block txt-center m-t-sm">
        <button
            type="button"
            class="btn btn-sm btn-secondary"
            class:btn-disabled={!hasPrevPage || isLoading}
            disabled={!hasPrevPage || isLoading}
            on:click={goToFirstPage}
        >
            <span class="txt">First</span>
        </button>
        <button
            type="button"
            class="btn btn-sm btn-secondary"
            class:btn-disabled={!hasPrevPage || isLoading}
            disabled={!hasPrevPage || isLoading}
            on:click={goToPreviousPage}
        >
            <span class="txt">Previous</span>
        </button>

        <span class="pagination-info">
            Page {currentPage} of {totalPages} · {totalItems} total
        </span>

        <button
            type="button"
            class="btn btn-sm btn-secondary"
            class:btn-disabled={!hasNextPage || isLoading}
            disabled={!hasNextPage || isLoading}
            on:click={goToNextPage}
        >
            <span class="txt">Next</span>
        </button>
        <button
            type="button"
            class="btn btn-sm btn-secondary"
            class:btn-disabled={!hasNextPage || isLoading}
            disabled={!hasNextPage || isLoading}
            on:click={goToLastPage}
        >
            <span class="txt">Last</span>
        </button>
    </div>
{/if}

{#if totalBulkSelected}
    <div class="bulkbar" transition:fly={{ duration: 150, y: 5 }}>
        <div class="txt">
            {$_("common.message.selectedPrompt", { values: { selected: totalBulkSelected } })}
        </div>
        <button
            type="button"
            class="btn btn-xs btn-transparent btn-outline p-l-5 p-r-5"
            on:click={() => deselectAllLogs()}
        >
            <span class="txt">{$_("common.action.reset")}</span>
        </button>
        <div class="flex-fill" />
        <button type="button" class="btn btn-sm" on:click={downloadSelected}>
            <span class="txt">{$_("common.action.exportJson")}</span>
        </button>
    </div>
{/if}

<style>
    .bulkbar {
        position: sticky;
        margin-top: var(--smSpacing);
        bottom: var(--baseSpacing);
    }
    .col-field-level {
        min-width: 100px;
    }
    .col-field-data {
        max-width: 320px;
        white-space: nowrap;
    }
    .col-field-data .txt-mono {
        display: inline-block;
        max-width: 100%;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }
    .pagination {
        display: flex;
        justify-content: center;
        align-items: center;
        gap: var(--smSpacing);
        flex-wrap: wrap;
    }
    .pagination-info {
        font-size: var(--smFontSize);
        color: var(--txtHintColor);
    }
    .col-field-message {
        min-width: 600px;
    }
</style>
