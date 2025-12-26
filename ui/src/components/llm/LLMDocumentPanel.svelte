<script>
    import { _ } from "svelte-i18n";
    import { createEventDispatcher } from "svelte";
    import ApiClient from "@/utils/ApiClient";
    import { addSuccessToast } from "@/stores/toasts";
    import OverlayPanel from "@/components/base/OverlayPanel.svelte";
    import RefreshButton from "@/components/base/RefreshButton.svelte";
    import LLMDocumentUpsertPanel from "./LLMDocumentUpsertPanel.svelte";

    const dispatch = createEventDispatcher();

    let managementPanel;
    let upsertPanel;
    let collection = null;
    let documents = [];
    let isLoading = false;
    let page = 1;
    let perPage = 25;
    let totalItems = 0;
    const perPageOptions = [10, 25, 50, 100];

    export function show(collectionData) {
        collection = collectionData;
        page = 1;
        perPage = 25;
        managementPanel?.show();
        loadDocuments();
    }

    function hide() {
        managementPanel?.hide();
    }

    async function loadDocuments() {
        if (!collection) {
            return;
        }

        isLoading = true;
        try {
            const response = await ApiClient.send(
                `/api/llm-documents/${encodeURIComponent(collection.name)}?page=${page}&perPage=${perPage}`,
                { method: "GET" },
            );
            documents = response.items || [];
            totalItems = response.totalItems ?? documents.length;

            const resolvedPage = response.page || page;
            page = resolvedPage;

            const computedTotalPages = Math.max(1, Math.ceil((totalItems || 0) / perPage));
            if (totalItems === 0) {
                page = 1;
            } else if (page > computedTotalPages) {
                page = computedTotalPages;
                isLoading = false;
                await loadDocuments();
                return;
            }
        } catch (err) {
            ApiClient.error(err);
        }
        isLoading = false;
    }

    function openCreatePanel() {
        upsertPanel?.show(collection, null);
    }

    function editDocument(doc) {
        upsertPanel?.show(collection, doc);
    }

    async function deleteDocument(id) {
        if (!confirm($_("page.llm.documents.confirmDelete"))) {
            return;
        }

        try {
            await ApiClient.send(
                `/api/llm-documents/${encodeURIComponent(collection.name)}/${encodeURIComponent(id)}`,
                { method: "DELETE" },
            );
            addSuccessToast($_("page.llm.documents.toast.deleted"));
            if (documents.length <= 1 && page > 1) {
                page -= 1;
            }
            await loadDocuments();
            dispatch("changed");
        } catch (err) {
            ApiClient.error(err);
        }
    }

    function handleDocumentSaved(event) {
        const isEdit = event?.detail?.isEdit;
        if (!isEdit) {
            page = 1;
        }
        loadDocuments();
        dispatch("changed");
    }

    function changePage(direction) {
        const target = page + direction;
        if (target < 1 || target > totalPages) {
            return;
        }
        page = target;
        loadDocuments();
    }

    function handlePerPageChange(event) {
        perPage = parseInt(event.target.value, 10) || 25;
        page = 1;
        loadDocuments();
    }

    $: totalPages = Math.max(1, Math.ceil((totalItems || 0) / perPage));
    $: paginationSummary = (() => {
        const template = $_("page.llm.documents.pagination.summary");
        return template
            .replace("{current}", page)
            .replace("{total}", totalPages)
            .replace("{count}", totalItems);
    })();
</script>

<OverlayPanel
    bind:this={managementPanel}
    title={$_("page.llm.documents.title", { name: collection?.name || "" })}
    class="llm-document-management-panel overlay-panel-xl"
    popup={true}
>
    <header class="panel-header">
        <div class="panel-header-left">
            <button type="button" class="btn btn-sm" on:click={openCreatePanel}>
                <i class="ri-add-line" />
                <span class="txt">{$_("page.llm.documents.actions.add")}</span>
            </button>
        </div>
        <div class="panel-header-right">
            <label class="txt-sm txt-hint m-r-sm" for="llm-per-page">
                {$_("page.llm.documents.perPage")}
            </label>
            <select id="llm-per-page" bind:value={perPage} on:change={handlePerPageChange}>
                {#each perPageOptions as option}
                    <option value={option}>{option}</option>
                {/each}
            </select>
            <RefreshButton class="m-l-sm" on:refresh={loadDocuments} />
        </div>
    </header>

    {#if isLoading}
        <div class="m-t-xl m-b-xl txt-center txt-hint">
            <i class="ri-loader-4-line spin" />
            <span class="m-l-sm">{$_("page.llm.documents.loading")}</span>
        </div>
    {:else if documents.length === 0}
        <div class="m-t-xl m-b-xl txt-center txt-hint">
            <p>{$_("page.llm.documents.noData")}</p>
            <p class="m-t-sm">{$_("page.llm.documents.noDataHint")}</p>
        </div>
    {:else}
        <div class="table-wrapper">
            <table class="table">
                <thead>
                    <tr>
                        <th>{$_("page.llm.documents.table.id")}</th>
                        <th>{$_("page.llm.documents.table.content")}</th>
                        <th>{$_("page.llm.documents.table.metadata")}</th>
                        <th>{$_("page.llm.documents.table.embedding")}</th>
                        <th class="txt-right">{$_("page.llm.documents.table.actions")}</th>
                    </tr>
                </thead>
                <tbody>
                    {#each documents as document}
                        <tr>
                            <td><code class="txt-xs">{document.id}</code></td>
                            <td>
                                {#if document.content}
                                    <span class="txt-ellipsis" style="max-width: 400px; display: inline-block;">
                                        {document.content}
                                    </span>
                                {:else}
                                    <span class="txt-hint">-</span>
                                {/if}
                            </td>
                            <td>
                                {#if document.metadata && Object.keys(document.metadata).length}
                                    <code class="txt-xs" style="max-width: 300px; display: inline-block; word-break: break-all;">
                                        {JSON.stringify(document.metadata)}
                                    </code>
                                {:else}
                                    <span class="txt-hint">-</span>
                                {/if}
                            </td>
                            <td>
                                {#if document.embedding && document.embedding.length}
                                    <span class="badge badge-outline">{document.embedding.length}</span>
                                {:else}
                                    <span class="txt-hint">-</span>
                                {/if}
                            </td>
                            <td class="txt-right">
                                <button
                                    type="button"
                                    class="btn btn-transparent btn-sm"
                                    on:click={() => editDocument(document)}
                                    title={$_("page.llm.documents.table.edit")}
                                >
                                    <i class="ri-edit-line" />
                                </button>
                                <button
                                    type="button"
                                    class="btn btn-transparent btn-sm txt-danger"
                                    on:click={() => deleteDocument(document.id)}
                                    title={$_("page.llm.documents.table.delete")}
                                >
                                    <i class="ri-delete-bin-line" />
                                </button>
                            </td>
                        </tr>
                    {/each}
                </tbody>
            </table>
        </div>

        <div class="pagination m-t-lg">
            <button type="button" class="btn btn-sm" disabled={page <= 1} on:click={() => changePage(-1)}>
                <i class="ri-arrow-left-line" />
                <span class="txt">{$_("page.llm.documents.pagination.previous")}</span>
            </button>
            <span class="txt-hint txt-sm m-l-sm m-r-sm">
                {paginationSummary}
            </span>
            <button type="button" class="btn btn-sm" disabled={page >= totalPages} on:click={() => changePage(1)}>
                <span class="txt">{$_("page.llm.documents.pagination.next")}</span>
                <i class="ri-arrow-right-line" />
            </button>
        </div>
    {/if}
</OverlayPanel>

<LLMDocumentUpsertPanel bind:this={upsertPanel} {collection} on:saved={handleDocumentSaved} />

<style>
    .panel-header {
        display: flex;
        align-items: center;
        justify-content: space-between;
        margin-bottom: 1rem;
        flex-wrap: wrap;
        gap: 0.5rem;
    }

    .panel-header-right select {
        min-width: 80px;
    }

    .pagination {
        display: flex;
        align-items: center;
        justify-content: center;
    }

    .table-wrapper {
        overflow-x: auto;
    }

    .table {
        min-width: 100%;
    }

    .table td {
        word-break: break-word;
    }

    .spin {
        animation: spin 1s linear infinite;
    }

    @keyframes spin {
        from {
            transform: rotate(0deg);
        }
        to {
            transform: rotate(360deg);
        }
    }
</style>
