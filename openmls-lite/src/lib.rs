use openmls::prelude::*;
use wasm_bindgen::{prelude::*, JsCast};

use openmls::group::{MlsGroup, MlsGroupCreateConfig, StagedWelcome};
use openmls_basic_credential::SignatureKeyPair;
use openmls_traits::OpenMlsProvider;
use tls_codec::{Serialize, Deserialize};
use std::rc::Rc;
use std::collections::HashMap;

#[cfg(target_arch = "wasm32")]
type Provider = openmls_libcrux_crypto::Provider;
#[cfg(not(target_arch = "wasm32"))]
type Provider = openmls_rust_crypto::OpenMlsRustCrypto;

#[wasm_bindgen]
pub struct Client {
    name: String,
    provider: Rc<Provider>,
    signer: Rc<SignatureKeyPair>,
    credential: CredentialWithKey,
}

#[wasm_bindgen]
impl Client {
    #[wasm_bindgen(constructor)]
    pub fn new(name: String) -> Result<Client, JsValue> {
        let provider = Rc::new(Provider::default());
        let suite = Ciphersuite::MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519;
        let (credential, signer) = generate_credential_with_key(
            name.as_bytes().to_vec(),
            CredentialType::Basic,
            suite.signature_algorithm(),
            provider.as_ref(),
        )?;
        Ok(Client {name, provider, signer: Rc::new(signer), credential})
    }

    #[wasm_bindgen]
    pub fn create_group(&self) -> Result<Group, JsValue> {
        let group = MlsGroup::new(
            self.provider.as_ref(),
            self.signer.as_ref(),
            &MlsGroupCreateConfig::builder()
                .use_ratchet_tree_extension(true)
                .build(),
            self.credential.clone(),
        )
        .map_err(err)?;
        Ok(Group { group: group, provider: self.provider.clone(),  signer: self.signer.clone()})
    }

    #[wasm_bindgen]
    pub fn key_package(&self) -> Result<Vec<u8>, JsValue> {
        let suite = Ciphersuite::MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519;
        let kp = generate_key_package(suite, self.provider.as_ref(), self.signer.as_ref(), self.credential.clone())?;
        kp.tls_serialize_detached().map_err(err)
    }

    #[wasm_bindgen]
    pub fn join_from_welcome(
        &self,
        welcome_bytes: &[u8],
        ratchet_tree_bytes: Option<Box<[u8]>>
    ) -> Result<Group, JsValue> {
        let mls_message_in = MlsMessageIn::tls_deserialize(&mut welcome_bytes.as_ref()).map_err(err)?;
        let welcome = match mls_message_in.extract() {
            MlsMessageBodyIn::Welcome(welcome) => welcome,
            _ => unreachable!("Unexpected message type, expected Welcome."),
        };

        // Build a join config that expects the ratchet tree extension in the Welcome
        let join_cfg = MlsGroupJoinConfig::builder()
            .use_ratchet_tree_extension(true)
            .build();

        let tree = ratchet_tree_bytes
            .map(|b| RatchetTreeIn::tls_deserialize(&mut b.as_ref()))
            .transpose()
            .map_err(err)?;

        let staged = StagedWelcome::new_from_welcome(self.provider.as_ref(), &join_cfg, welcome, tree)
            .map_err(err)?;
        Ok(Group { group: staged.into_group(self.provider.as_ref()).map_err(err)?, provider: self.provider.clone(), signer: self.signer.clone() })
    }

    #[wasm_bindgen]
    pub fn export_storage_snapshot(&self) -> Result<Vec<u8>, JsValue> {
        let values = self
            .provider
            .storage()
            .values
            .read()
            .map_err(|_| JsValue::from_str("Failed to read MLS storage"))?;
        encode_storage_snapshot(&values)
    }

    #[wasm_bindgen]
    pub fn import_storage_snapshot(&self, snapshot: &[u8]) -> Result<(), JsValue> {
        let decoded = decode_storage_snapshot(snapshot)?;
        let mut values = self
            .provider
            .storage()
            .values
            .write()
            .map_err(|_| JsValue::from_str("Failed to write MLS storage"))?;
        values.clear();
        values.extend(decoded);
        Ok(())
    }

    #[wasm_bindgen]
    pub fn load_group(&self, group_id_bytes: &[u8]) -> Result<Group, JsValue> {
        let group_id = GroupId::from_slice(group_id_bytes);
        let group = MlsGroup::load(self.provider.storage(), &group_id)
            .map_err(err)?
            .ok_or_else(|| JsValue::from_str("MLS group not found in storage"))?;

        // Read signer identity from restored group state.
        let sig_pub = group
            .own_leaf_node()
            .ok_or_else(|| JsValue::from_str("MLS group missing own leaf node"))?
            .signature_key()
            .as_slice()
            .to_vec();

        let sig_scheme = group.ciphersuite().signature_algorithm();

        // Reconstruct the matching private signing key from provider storage.
        let signer = SignatureKeyPair::read(self.provider.storage(), &sig_pub, sig_scheme)
            .ok_or_else(|| JsValue::from_str("MLS signer keypair not found in storage"))?;

        Ok(Group {
            group,
            provider: self.provider.clone(),
            signer: Rc::new(signer),
        })
    }
}


#[wasm_bindgen]
pub struct Group {
    group: MlsGroup,
    provider: Rc<Provider>,
    signer: Rc<SignatureKeyPair>,
}

#[wasm_bindgen]
impl Group {
    fn provider(&self) -> &Provider { self.provider.as_ref() }

    #[wasm_bindgen]
    pub fn apply_commit(&mut self, commit_bytes: &[u8]) -> Result<(), JsValue> {
        let mut input = commit_bytes;
        let msg_in = MlsMessageIn::tls_deserialize(&mut input).map_err(err)?;

        let protocol = msg_in.try_into_protocol_message().map_err(err)?;
        let processed = self
            .group
            .process_message(self.provider.as_ref(), protocol)
            .map_err(err)?;

        // For peer-applied commits, process_message yields a staged commit that must be merged.
        match processed.into_content() {
            ProcessedMessageContent::StagedCommitMessage(staged_commit) => {
                self.group
                    .merge_staged_commit(self.provider.as_ref(), *staged_commit)
                    .map_err(err)?;
                Ok(())
            }
            other => Err(JsValue::from_str(&format!(
                "Expected commit message, got {other:?}"
            ))),
        }
    }
    #[wasm_bindgen]
    pub fn merge_pending_commit(&mut self) -> Result<(), JsValue> {
        let provider = self.provider.clone();
        self.group.merge_pending_commit(provider.as_ref()).map_err(err)
    }

    #[wasm_bindgen]
    pub fn my_index(&self) -> u32 {
        self.group.own_leaf_index().u32()
    }

    #[wasm_bindgen]
    pub fn group_id_bytes(&self) -> Vec<u8> {
        self.group.group_id().as_slice().to_vec()
    }

    #[wasm_bindgen]
    pub fn epoch(&self) -> u64 {
        self.group.epoch().as_u64()
    }

    #[wasm_bindgen]
    pub fn add_members(&mut self, key_packages: Box<[JsValue]>) -> Result<JsValue, JsValue> {
    let kp_vec: Vec<KeyPackage> = key_packages.iter()
        .map(|v| -> Result<_, JsValue> {
            let bytes = v.clone().dyn_into::<js_sys::Uint8Array>()?.to_vec();
            let kp_in = KeyPackageIn::tls_deserialize(&mut bytes.as_slice()).map_err(err)?;
            let kp = kp_in.validate(self.provider().crypto(), ProtocolVersion::Mls10).map_err(err)?;
            Ok(kp)
        })
        .collect::<Result<_, _>>()?;
        let (commit, welcome, group_info_opt) = self.group.add_members(self.provider.as_ref(), self.signer.as_ref(), &kp_vec).map_err(err)?;
        let out = js_sys::Object::new();
        let commit_bytes = commit.tls_serialize_detached().map_err(err)?;
        let welcome_bytes = welcome.tls_serialize_detached().map_err(err)?;
        js_sys::Reflect::set(&out, &"commit".into(), &js_sys::Uint8Array::from(commit_bytes.as_slice()).into())?;
        js_sys::Reflect::set(&out, &"welcome".into(), &js_sys::Uint8Array::from(welcome_bytes.as_slice()).into())?;
        Ok(out.into())
    }

    #[wasm_bindgen]
    pub fn remove_member(&mut self, leaves: Box<[u32]>) -> Result<JsValue, JsValue> {
        let idxs: Vec<LeafNodeIndex> = leaves.iter().map(|i| LeafNodeIndex::new(*i)).collect();
        let (commit, mls_msg_out,group_info_opt) =
            self.group.remove_members(self.provider.as_ref(), self.signer.as_ref(), &idxs)
                .map_err(err)?;
        let out = js_sys::Object::new();
        let commit_bytes = commit.tls_serialize_detached().map_err(err)?;
        js_sys::Reflect::set(&out, &"commit".into(),
            &js_sys::Uint8Array::from(commit_bytes.as_slice()).into())?;
        Ok(out.into())
    }

    #[wasm_bindgen]
    pub fn member_index_by_identity(&self, identity: &[u8]) -> Option<u32> {
        self.group
            .members()
            .find(|m| {
                m.credential.credential_type() == CredentialType::Basic &&
                m.credential.serialized_content() == identity
            })
            .map(|m| m.index.u32())
    }
    
    #[wasm_bindgen]
    pub fn export_secret(&self, label: &str, context: &[u8], len: usize) -> Result<Vec<u8>, JsValue> {
        self.group.export_secret(self.provider().crypto(), label, context, len).map_err(err)
    }
}

fn generate_credential_with_key(
    identity: Vec<u8>,
    _credential_type: CredentialType,
    signature_algorithm: SignatureScheme,
    provider: &impl OpenMlsProvider,
) -> Result<(CredentialWithKey, SignatureKeyPair), JsValue> {
    let credential = BasicCredential::new(identity);
    let signature_keys = SignatureKeyPair::new(signature_algorithm).map_err(err)?;
    signature_keys.store(provider.storage()).map_err(err)?;
    Ok((
        CredentialWithKey {
            credential: credential.into(),
            signature_key: signature_keys.public().into(),
        },
        signature_keys,
    ))
}

fn generate_key_package(
    ciphersuite: Ciphersuite,
    provider: &impl OpenMlsProvider,
    signer: &SignatureKeyPair,
    credential_with_key: CredentialWithKey,
) -> Result<KeyPackage, JsValue> {
    let bundle = KeyPackage::builder()
        .build(ciphersuite, provider, signer, credential_with_key)
        .map_err(err)?;
    Ok(bundle.key_package().clone())
}

fn err<E: core::fmt::Debug>(e: E) -> JsValue {
    JsValue::from_str(&format!("{e:?}"))
}

fn append_u32_le(out: &mut Vec<u8>, value: usize) -> Result<(), JsValue> {
    let value = u32::try_from(value).map_err(|_| JsValue::from_str("Length does not fit in u32"))?;
    out.extend_from_slice(&value.to_le_bytes());
    Ok(())
}

fn encode_storage_snapshot(values: &HashMap<Vec<u8>, Vec<u8>>) -> Result<Vec<u8>, JsValue> {
    let mut out = Vec::new();
    append_u32_le(&mut out, values.len())?;
    for (k, v) in values {
        append_u32_le(&mut out, k.len())?;
        out.extend_from_slice(k);
        append_u32_le(&mut out, v.len())?;
        out.extend_from_slice(v);
    }
    Ok(out)
}

fn read_u32_le(input: &[u8], cursor: &mut usize) -> Result<usize, JsValue> {
    if input.len().saturating_sub(*cursor) < 4 {
        return Err(JsValue::from_str("Truncated MLS snapshot (missing u32)"));
    }
    let mut buf = [0u8; 4];
    buf.copy_from_slice(&input[*cursor..*cursor + 4]);
    *cursor += 4;
    Ok(u32::from_le_bytes(buf) as usize)
}

fn read_vec(input: &[u8], cursor: &mut usize, len: usize) -> Result<Vec<u8>, JsValue> {
    if input.len().saturating_sub(*cursor) < len {
        return Err(JsValue::from_str("Truncated MLS snapshot (missing payload bytes)"));
    }
    let out = input[*cursor..*cursor + len].to_vec();
    *cursor += len;
    Ok(out)
}

fn decode_storage_snapshot(snapshot: &[u8]) -> Result<HashMap<Vec<u8>, Vec<u8>>, JsValue> {
    let mut cursor = 0usize;
    let count = read_u32_le(snapshot, &mut cursor)?;
    let mut out = HashMap::with_capacity(count);

    for _ in 0..count {
        let k_len = read_u32_le(snapshot, &mut cursor)?;
        let key = read_vec(snapshot, &mut cursor, k_len)?;
        let v_len = read_u32_le(snapshot, &mut cursor)?;
        let val = read_vec(snapshot, &mut cursor, v_len)?;
        out.insert(key, val);
    }

    if cursor != snapshot.len() {
        return Err(JsValue::from_str("Invalid MLS snapshot (trailing bytes)"));
    }
    Ok(out)
}
