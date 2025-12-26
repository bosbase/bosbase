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

    $: backendAbsUrl = CommonHelper.getApiExampleUrl(ApiClient.baseURL);

    $: identityFields = collection?.passwordAuth?.identityFields || [];

    $: exampleIdentityLabel =
        identityFields.length == 0 ? "NONE" : "YOUR_" + identityFields.join("_OR_").toUpperCase();

    $: responses = [
        {
            code: 200,
            body: JSON.stringify(
                {
                    token: "JWT_TOKEN",
                    record: CommonHelper.dummyCollectionRecord(collection),
                },
                null,
                2,
            ),
        },
        {
            code: 400,
            body: `
                {
                  "code": 400,
                  "message": "Failed to authenticate.",
                  "data": {
                    "identity": {
                      "code": "validation_required",
                      "message": "Missing required value."
                    }
                  }
                }
            `,
        },
    ];
</script>

<h3 class="m-b-sm">
    {$_("common.popup.apiDocs.authWithPassword.name")}
</h3>
<div class="content txt-lg m-b-sm">
    <p>{$_("common.popup.apiDocs.authWithPassword.content.1", { values: { tableName: collection.name } })}</p>
</div>

<SdkTabs
    js={`
        import BosBase from 'bosbase';

        const pb = new BosBase('${backendAbsUrl}');

        ...

        const authData = await pb.collection('${collection?.name}').authWithPassword(
            '${exampleIdentityLabel}',
            'YOUR_PASSWORD',
        );

        // after the above you can also access the auth data from the authStore
        console.log(pb.authStore.isValid);
        console.log(pb.authStore.token);
        console.log(pb.authStore.record.id);

        // "logout"
        pb.authStore.clear();
    `}
    dart={`
        import 'package:bosbase/bosbase.dart';

        final pb = BosBase('${backendAbsUrl}');

        ...

        final authData = await pb.collection('${collection?.name}').authWithPassword(
          '${exampleIdentityLabel}',
          'YOUR_PASSWORD',
        );

        // after the above you can also access the auth data from the authStore
        print(pb.authStore.isValid);
        print(pb.authStore.token);
        print(pb.authStore.record.id);

        // "logout"
        pb.authStore.clear();
    `}
/>

<h6 class="m-b-xs">{$_("common.placeholder.apiUrl")}</h6>
<div class="alert alert-success">
    <strong class="label label-primary">POST</strong>
    <div class="content">
        <p>
            /api/collections/<strong>{collection.name}</strong>/auth-with-password
        </p>
    </div>
</div>

<div class="section-title">{$_("common.placeholder.apiParameters")}</div>
<table class="table-compact table-border m-b-base">
    <thead>
        <tr>
            <th>{$_("common.placeholder.params")}</th>
            <th>{$_("common.placeholder.type")}</th>
            <th width="50%">{$_("common.placeholder.description")}</th>
        </tr>
    </thead>
    <tbody>
        <tr>
            <td>
                <div class="inline-flex">
                    <span class="label label-success">{$_("common.tip.required")}</span>
                    <span>identity</span>
                </div>
            </td>
            <td>
                <span class="label">String</span>
            </td>
            <td>
                {#each identityFields as name, i}
                    {#if i > 0}or{/if}
                    <strong>{name}</strong>
                {/each}
                of the record to authenticate.
            </td>
        </tr>
        <tr>
            <td>
                <div class="inline-flex">
                    <span class="label label-success">{$_("common.tip.required")}</span>
                    <span>password</span>
                </div>
            </td>
            <td>
                <span class="label">String</span>
            </td>
            <td>The auth record password.</td>
        </tr>
    </tbody>
</table>

<div class="section-title">{$_("common.placeholder.apiQueryParameters")}</div>
<table class="table-compact table-border m-b-base">
    <thead>
        <tr>
            <th>{$_("common.placeholder.params")}</th>
            <th>{$_("common.placeholder.type")}</th>
            <th width="60%">{$_("common.placeholder.description")}</th>
        </tr>
    </thead>
    <tbody>
        <tr>
            <td>expand</td>
            <td>
                <span class="label">String</span>
            </td>
            <td>
                Auto expand record relations. Ex.:
                <CodeBlock content={`?expand=relField1,relField2.subRelField`} />
                Supports up to 6-levels depth nested relations expansion. <br />
                The expanded relations will be appended to the record under the
                <code>expand</code> property (eg. <code>{`"expand": {"relField1": {...}, ...}`}</code>).
                <br />
                Only the relations to which the request user has permissions to <strong>view</strong> will be expanded.
            </td>
        </tr>
        <FieldsQueryParam prefix="record." />
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
