# type:ignore

import pandas as pd
from sklearn.preprocessing import RobustScaler, StandardScaler

from .time_series import preprocess


def nsigma(
    data: pd.DataFrame,
    inject_time: int | None = None,
    dataset: str | None = None,
    anomalies: list[int] | None = None,
    dk_select_useful: bool = False,
):
    if anomalies is None:
        normal_df = data[data["time"] < inject_time]
        anomal_df = data[data["time"] >= inject_time]
    else:
        normal_df = data.head(anomalies[0])
        # anomal_df is the rest
        anomal_df = data.tail(len(data) - anomalies[0])

    normal_df = preprocess(data=normal_df, dataset=dataset, dk_select_useful=dk_select_useful)

    anomal_df = preprocess(data=anomal_df, dataset=dataset, dk_select_useful=dk_select_useful)

    # intersect
    intersects = [x for x in normal_df.columns if x in anomal_df.columns]
    normal_df = normal_df[intersects]
    anomal_df = anomal_df[intersects]

    ranks = []

    for col in normal_df.columns:
        a = normal_df[col].to_numpy()
        b = anomal_df[col].to_numpy()

        scaler = StandardScaler().fit(a.reshape(-1, 1))
        zscores = scaler.transform(b.reshape(-1, 1))[:, 0]
        score = max(zscores)
        ranks.append((col, score))

    ranks = sorted(ranks, key=lambda x: x[1], reverse=True)
    ranks = [x[0] for x in ranks]

    return {
        "node_names": normal_df.columns.to_list(),
        "ranks": ranks,
    }
