<script>
    import { _ } from "svelte-i18n";
    import { onMount } from "svelte";
    import { pageTitle } from "@/stores/app";
    import ApiClient from "@/utils/ApiClient";
    import { addSuccessToast } from "@/stores/toasts";
    import { setErrors } from "@/stores/errors";
    import PageWrapper from "@/components/base/PageWrapper.svelte";
    import OverlayPanel from "@/components/base/OverlayPanel.svelte";
    import Field from "@/components/base/Field.svelte";
    import RefreshButton from "@/components/base/RefreshButton.svelte";
    import Searchbar from "@/components/base/Searchbar.svelte";
    import LLMDocumentPanel from "./LLMDocumentPanel.svelte";

    $pageTitle = $_("page.llm.title");

    let allCollections = [];
    let filteredCollections = [];
    let visibleCollections = [];
    let isLoading = false;
    let searchFilter = "";
    let createPanel;
    let documentPanel;
    let page = 1;
    let perPage = 10;

    const defaultMetadataPlaceholder = '{"domain": "internal"}';

    let formData = {
        name: "",
        metadata: "{}",
    };

    onMount(() => {
        loadCollections();
    });

    async function loadCollections() {
        isLoading = true;
        try {
            const response = await ApiClient.send("/api/llm-documents/collections", {
                method: "GET",
            });
            allCollections = response || [];
            applySearchFilter();
        } catch (err) {
            ApiClient.error(err);
        }
        isLoading = false;
    }

    function applySearchFilter() {
        let working = allCollections;

        if (searchFilter.trim()) {
            const needle = searchFilter.trim().toLowerCase();
            working = allCollections.filter((collection) => {
                if (collection.id?.toLowerCase().includes(needle)) {
                    return true;
                }

                if (collection.name?.toLowerCase().includes(needle)) {
                    return true;
                }

                if (collection.metadata) {
                    const metadataText = JSON.stringify(collection.metadata).toLowerCase();
                    return metadataText.includes(needle);
                }

                return false;
            });
        }

        working = [...working].sort((a, b) => {
            const aId = (a.id || "").toLowerCase();
            const bId = (b.id || "").toLowerCase();
            return bId.localeCompare(aId);
        });

        filteredCollections = working;
        page = 1;
        updatePagination();
    }

    function handleSearch(e) {
        searchFilter = e.detail || "";
        applySearchFilter();
    }

    function clearSearch() {
        searchFilter = "";
        applySearchFilter();
    }

    function showCreatePanel() {
        formData = { name: "", metadata: "{}" };
        setErrors({});
        createPanel?.show();
    }

    function updatePagination() {
        const totalItems = filteredCollections.length;
        const totalPages = Math.max(1, Math.ceil(totalItems / perPage));
        if (page > totalPages) {
            page = totalPages;
        }

        const start = (page - 1) * perPage;
        visibleCollections = filteredCollections.slice(start, start + perPage);
    }

    function changePage(delta) {
        const totalPages = Math.max(1, Math.ceil(filteredCollections.length / perPage));
        const nextPage = Math.min(Math.max(page + delta, 1), totalPages);
        if (nextPage !== page) {
            page = nextPage;
            updatePagination();
        }
    }

    function changePerPage(event) {
        perPage = parseInt(event.target.value, 10) || 10;
        page = 1;
        updatePagination();
    }

    $: totalCollections = filteredCollections.length;
    $: totalPages = Math.max(1, Math.ceil(totalCollections / perPage));
    $: paginationSummary = (() => {
        const template = $_("page.llm.collections.pagination.summary");
        return template
            .replace("{current}", page)
            .replace("{total}", totalPages)
            .replace("{count}", totalCollections);
    })();
    $: paginationSummarySingle = (() => {
        const template = $_("page.llm.collections.pagination.summary");
        return template
            .replace("{current}", "1")
            .replace("{total}", "1")
            .replace("{count}", totalCollections);
    })();

    function normalizeMetadata(value, errorsBag) {
        if (!value || !value.trim()) {
            return {};
        }

        try {
            const parsed = JSON.parse(value);
            if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
                throw new Error("invalid");
            }

            const normalized = {};
            Object.entries(parsed).forEach(([key, val]) => {
                normalized[key] = val === undefined || val === null ? "" : String(val);
            });
            return normalized;
        } catch (err) {
            errorsBag.metadata = $_("page.llm.collections.panel.metadataInvalid");
            return null;
        }
    }

    async function createCollection() {
        const errorsBag = {};

        if (!formData.name.trim()) {
            errorsBag.name = $_("page.llm.collections.panel.nameRequired");
        }

        const metadata = normalizeMetadata(formData.metadata || "", errorsBag);

        if (Object.keys(errorsBag).length) {
            setErrors(errorsBag);
            return;
        }

        try {
            await ApiClient.send(`/api/llm-documents/collections/${encodeURIComponent(formData.name.trim())}`, {
                method: "POST",
                body: {
                    metadata,
                },
            });
            addSuccessToast($_("page.llm.collections.toast.created"));
            createPanel?.hide();
            formData = { name: "", metadata: "{}" };
            setErrors({});
            await loadCollections();
        } catch (err) {
            ApiClient.error(err);
        }
    }

    async function deleteCollection(name) {
        if (!confirm($_("page.llm.collections.confirmDelete", { name }))) {
            return;
        }

        try {
            await ApiClient.send(`/api/llm-documents/collections/${encodeURIComponent(name)}`, {
                method: "DELETE",
            });
            addSuccessToast($_("page.llm.collections.toast.deleted"));
            await loadCollections();
        } catch (err) {
            ApiClient.error(err);
        }
    }

    function manageCollection(collection) {
        documentPanel?.show(collection);
    }
</script>

<PageWrapper class="flex-content">
    <header class="page-header">
        <nav class="breadcrumbs">
            <div class="breadcrumb-item">{$pageTitle}</div>
        </nav>

        <div class="inline-flex gap-5">
            <RefreshButton on:refresh={loadCollections} />
        </div>

        <div class="btns-group">
            <button type="button" class="btn btn-expanded" on:click={showCreatePanel}>
                <i class="ri-add-line" />
                <span class="txt">{$_("page.llm.collections.actions.create")}</span>
            </button>
        </div>
    </header>

    <Searchbar
        value={searchFilter}
        placeholder={$_("page.llm.collections.searchPlaceholder")}
        on:submit={handleSearch}
        on:clear={clearSearch}
    />

    <div class="clearfix m-b-sm" />

    {#if isLoading}
        <div class="m-t-xl m-b-xl txt-center txt-hint">
            <i class="ri-loader-4-line spin" />
            <span class="m-l-sm">{$_("page.llm.collections.loading")}</span>
        </div>
    {:else if filteredCollections.length === 0}
        <div class="m-t-xl m-b-xl txt-center txt-hint">
            {#if searchFilter}
                <p>{$_("page.llm.collections.noMatch", { value: searchFilter })}</p>
                <p class="m-t-sm">
                    <button type="button" class="btn btn-sm btn-outline" on:click={clearSearch}>
                        {$_("page.llm.collections.actions.clearSearch")}
                    </button>
                </p>
            {:else}
                <p>{$_("page.llm.collections.noData")}</p>
                <p class="m-t-sm">{$_("page.llm.collections.noDataHint")}</p>
            {/if}
        </div>
    {:else}
        <div class="table-wrapper">
            <table class="table">
				<thead>
					<tr>
						<th>{$_("page.llm.collections.table.id")}</th>
						<th>{$_("page.llm.collections.table.name")}</th>
						<th>{$_("page.llm.collections.table.metadata")}</th>
						<th>{$_("page.llm.collections.table.documents")}</th>
						<th class="txt-right">{$_("page.llm.collections.table.actions")}</th>
					</tr>
                </thead>
					<tbody>
						{#each visibleCollections as collection}
						<tr>
							<td>
								{#if collection.id}
									<code class="txt-xs">{collection.id}</code>
								{:else}
									<span class="txt-hint">-</span>
								{/if}
							</td>
							<td>
								<strong>{collection.name}</strong>
							</td>
                            <td>
                                {#if collection.metadata && Object.keys(collection.metadata).length > 0}
                                    <code class="txt-xs">{JSON.stringify(collection.metadata)}</code>
                                {:else}
                                    <span class="txt-hint">-</span>
                                {/if}
                            </td>
                            <td>
                                <span class="badge">{collection.count ?? 0}</span>
                            </td>
                            <td class="txt-right">
                                <button
                                    type="button"
                                    class="btn btn-transparent btn-sm"
                                    on:click={() => manageCollection(collection)}
                                    title={$_("page.llm.collections.actions.manage")}
                                >
                                    <i class="ri-book-open-line" />
                                </button>
                                <button
                                    type="button"
                                    class="btn btn-transparent btn-sm txt-danger"
                                    on:click={() => deleteCollection(collection.name)}
                                    title={$_("page.llm.collections.actions.delete")}
                                >
                                    <i class="ri-delete-bin-line" />
                                </button>
                            </td>
                        </tr>
                    {/each}
                </tbody>
            </table>

            <div class="table-controls m-t-sm">
                <label class="controls-label">
                    {$_("page.llm.collections.pagination.perPage")}
                </label>
                <select class="form-control form-control-sm" on:change={changePerPage} bind:value={perPage}>
                    <option value="10">10</option>
                    <option value="25">25</option>
                    <option value="50">50</option>
                </select>
            </div>

            {#if totalPages > 1}
                <div class="table-pagination m-t-sm">
                    <button
                        type="button"
                        class="btn btn-sm btn-outline"
                        on:click={() => changePage(-1)}
                        disabled={page === 1}
                    >
                        {$_("page.llm.collections.pagination.previous")}
                    </button>
                    <span class="txt-hint m-l-sm m-r-sm">
                        {paginationSummary}
                    </span>
                    <button
                        type="button"
                        class="btn btn-sm btn-outline"
                        on:click={() => changePage(1)}
                        disabled={page === totalPages || totalCollections === 0}
                    >
                        {$_("page.llm.collections.pagination.next")}
                    </button>
                </div>
            {:else if totalCollections > 0}
                <div class="table-pagination m-t-sm">
                    <span class="txt-hint">
                        {paginationSummarySingle}
                    </span>
                </div>
            {/if}
        </div>
    {/if}
</PageWrapper>

<OverlayPanel bind:this={createPanel} title={$_("page.llm.collections.panel.createTitle")}>
    <form on:submit|preventDefault={createCollection}>
        <Field class="form-field required" name="name" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.llm.collections.panel.nameLabel")}</span>
            </label>
            <input
                type="text"
                id={uniqueId}
                placeholder={$_("page.llm.collections.panel.namePlaceholder")}
                bind:value={formData.name}
                maxlength="120"
            />
            <div class="help-block">
                <p>{$_("page.llm.collections.panel.nameHelp")}</p>
            </div>
        </Field>

        <Field class="form-field" name="metadata" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.llm.collections.panel.metadataLabel")}</span>
            </label>
            <textarea
                id={uniqueId}
                rows="5"
                placeholder={defaultMetadataPlaceholder}
                bind:value={formData.metadata}
                style="font-family: monospace; font-size: 0.9em;"
            />
            <div class="help-block">
                <p>{$_("page.llm.collections.panel.metadataHelp")}</p>
            </div>
        </Field>

        <div class="btns-group">
            <button type="submit" class="btn btn-expanded">
                <i class="ri-check-line" />
                <span class="txt">{$_("page.llm.collections.panel.create")}</span>
            </button>
        </div>
    </form>
</OverlayPanel>

<LLMDocumentPanel bind:this={documentPanel} on:changed={loadCollections} />

<style>
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
