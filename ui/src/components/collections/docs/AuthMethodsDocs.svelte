<script>
    import { _ } from "svelte-i18n";
    import CodeBlock from "@/components/base/CodeBlock.svelte";
    import FieldsQueryParam from "@/components/collections/docs/FieldsQueryParam.svelte";
    import SdkTabs from "@/components/base/SdkTabs.svelte";
    import ApiClient from "@/utils/ApiClient";
    import CommonHelper from "@/utils/CommonHelper";

    export let collection;

    let responseTab = 200;
    let responses = [];
    let authMethods = {};
    let isLoading = false;

    $: backendAbsUrl = CommonHelper.getApiExampleUrl(ApiClient.baseURL);

    $: responses = [
        {
            code: 200,
            body: isLoading ? "..." : JSON.stringify(authMethods, null, 2),
        },
        {
            code: 404,
            body: `
                {
                  "code": 404,
                  "message": "${$_("common.message.missingContext")}",
                  "data": {}
                }
            `,
        },
    ];

    listAuthMethods();

    async function listAuthMethods() {
        isLoading = true;
        try {
            authMethods = await ApiClient.collection(collection.name).listAuthMethods();
        } catch (err) {
            ApiClient.error(err);
        }
        isLoading = false;
    }
</script>

<h3 class="m-b-sm">
    {$_("common.popup.apiDocs.getAuthMethods.name")}
</h3>
<div class="content txt-lg m-b-sm">
    <p>{$_("common.popup.apiDocs.getAuthMethods.content.1", { values: { tableName: collection.name } })}</p>
</div>

<SdkTabs
    js={`
        import BosBase from 'bosbase';

        const pb = new BosBase('${backendAbsUrl}');

        ...

        const result = await pb.collection('${collection?.name}').listAuthMethods();
    `}
    dart={`
        import 'package:bosbase/bosbase.dart';

        final pb = BosBase('${backendAbsUrl}');

        ...

        final result = await pb.collection('${collection?.name}').listAuthMethods();
    `}
/>

<h6 class="m-b-xs">{$_("common.placeholder.apiUrl")}</h6>
<div class="alert alert-info">
    <strong class="label label-primary">GET</strong>
    <div class="content">
        <p>
            /api/collections/<strong>{collection.name}</strong>/auth-methods
        </p>
    </div>
</div>

<div class="section-title">{$_("common.placeholder.apiQueryParameters")}</div>
<table class="table-compact table-border m-b-base">
    <thead>
        <tr>
            <th>{$_("common.placeholder.params")}</th>
            <th>{$_("common.placeholder.type")}</th>
            <th width="50%">{$_("common.placeholder.description")}</th>
        </tr>
    </thead>
    <tbody>
        <FieldsQueryParam />
    </tbody>
</table>

<div class="section-title">{$_("common.placeholder.apiResponses")}</div>
<div class="tabs">
    <div class="tabs-header compact combined left">
        {#each responses as response (response.code)}
            <button
                class="tab-item"
                class:active={responseTab === response.code}
                on:click={() => (responseTab = response.code)}
            >
                {response.code}
            </button>
        {/each}
    </div>
    <div class="tabs-content">
        {#each responses as response (response.code)}
            <div class="tab-item" class:active={responseTab === response.code}>
                <CodeBlock content={response.body} />
            </div>
        {/each}
    </div>
</div>
